// SPDX-License-Identifier: AGPL-3.0-only

// Package testutil replays synthetic, recorded API-shaped responses entirely
// in memory. Unexpected requests fail closed; no transport opens a socket.
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
)

type Replay struct {
	T        *testing.T
	Provider string
	Fail     bool
	Empty    bool
	mu       sync.Mutex
	pages    int
}

func (r *Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, body, contentType := 200, "", "application/json"
	switch {
	case req.URL.Host == "login.microsoftonline.com" || req.URL.Host == "oauth2.googleapis.com":
		body = `{"access_token":"fixture-token","token_type":"Bearer","expires_in":3600}`
	case r.Provider == "azure" && req.URL.Host == "management.azure.com" && strings.HasSuffix(req.URL.Path, "/instanceView") && req.URL.Query().Get("api-version") == "2024-07-01":
		body = r.fixture("view.json")
	default:
		var token string
		switch r.Provider {
		case "aws":
			if req.URL.Host != "ec2.us-east-1.amazonaws.com" {
				return nil, fmt.Errorf("unexpected AWS host: %s", req.URL.Host)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			form, err := url.ParseQuery(string(data))
			if err != nil {
				return nil, err
			}
			if form.Get("Action") != "DescribeInstances" {
				return nil, fmt.Errorf("unexpected AWS action: %s", form.Get("Action"))
			}
			token = form.Get("NextToken")
			contentType = "text/xml"
		case "alibaba":
			if req.URL.Host != "ecs-cn-hangzhou.aliyuncs.com" {
				return nil, fmt.Errorf("unexpected Alibaba host: %s", req.URL.Host)
			}
			if req.Header.Get("x-acs-action") != "DescribeInstances" && !slices.Contains(req.Header["x-acs-action"], "DescribeInstances") && req.URL.Query().Get("Action") != "DescribeInstances" {
				return nil, fmt.Errorf("unexpected Alibaba action")
			}
			if req.URL.Query().Get("RegionId") != "cn-hangzhou" {
				return nil, fmt.Errorf("missing Alibaba region: %s", req.URL.String())
			}
			token = req.URL.Query().Get("NextToken")
		case "azure":
			if req.URL.Host != "management.azure.com" || !strings.HasSuffix(req.URL.Path, "/virtualMachines") || req.URL.Query().Get("api-version") != "2024-07-01" {
				return nil, fmt.Errorf("unexpected Azure request: %s", req.URL)
			}
			token = req.URL.Query().Get("$skiptoken")
		case "gcp":
			if req.URL.Host != "compute.googleapis.com" || !strings.HasSuffix(req.URL.Path, "/aggregated/instances") || req.URL.Query().Get("returnPartialSuccess") != "true" {
				return nil, fmt.Errorf("unexpected GCP request: %s", req.URL)
			}
			token = req.URL.Query().Get("pageToken")
		}
		expected := ""
		if r.pages == 1 {
			expected = "page-two"
		}
		if r.pages > 1 || token != expected {
			return nil, fmt.Errorf("unexpected page token %q at page %d", token, r.pages+1)
		}
		r.pages++
		ext := ".json"
		if r.Provider == "aws" {
			ext = ".xml"
		}
		file := fmt.Sprintf("page%d%s", r.pages, ext)
		if r.Empty {
			file = "empty" + ext
		}
		if r.Fail && r.pages == 2 {
			file = "error" + ext
			status = 403
		}
		body = r.fixture(file)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// Call implements the Alibaba SDK's injectable HTTP client.
func (r *Replay) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	return r.RoundTrip(req)
}

func (r *Replay) fixture(name string) string {
	r.T.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		r.T.Fatal(err)
	}
	return string(data)
}

func (r *Replay) Reset(fail, empty bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages = 0
	r.Fail = fail
	r.Empty = empty
}
func (r *Replay) AssertPages(n int) {
	r.T.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pages != n {
		r.T.Fatalf("inventory pages=%d, want %d", r.pages, n)
	}
}

// DefaultTransport is used only in serial adapter tests for cloud clients whose
// existing constructors use http.DefaultTransport. Restore after all requests.
func DefaultTransport(t *testing.T, replay *Replay) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = replay
	t.Cleanup(func() { http.DefaultTransport = original })
}

func GCPKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	data, err := json.Marshal(map[string]string{"client_email": "fixture@example.invalid", "private_key": string(private), "token_uri": "https://oauth2.googleapis.com/token"})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Check exercises the adapter and the legacy collector independently against
// exactly the same fixtures, including a failing continuation page.
func Check(t *testing.T, replay *Replay, adapter providers.Provider, account providers.Account, nativeType string, legacy func() ([]cloud.Instance, error)) {
	t.Helper()
	ctx := context.Background()
	specs := adapter.Services()
	if len(specs) != 1 || specs[0].Name != providers.Compute || !slices.Equal(specs[0].Kinds, []string{providers.Instance}) || specs[0].DefaultInterval <= 0 {
		t.Fatalf("services=%+v", specs)
	}
	specs[0].Kinds[0] = "mutated"
	if adapter.Services()[0].Kinds[0] != providers.Instance {
		t.Fatal("mutable service specification escaped")
	}

	resources, relations, err := adapter.List(ctx, account, providers.Compute, account.Region)
	if err != nil {
		t.Fatal(err)
	}
	replay.AssertPages(2)
	if len(resources) != 2 || len(relations) != 0 {
		t.Fatalf("resources=%d relations=%v", len(resources), relations)
	}
	replay.Reset(false, false)
	expected, err := legacy()
	if err != nil {
		t.Fatal(err)
	}
	replay.AssertPages(2)
	actual := make([]cloud.Instance, 0, len(resources))
	for _, item := range resources {
		if item.WorkspaceID != account.WorkspaceID || item.AccountID != account.ID || item.Provider != account.Provider || item.Service != providers.Compute || item.Kind != providers.Instance || item.SeenAt.IsZero() || item.SeenAt != resources[0].SeenAt {
			t.Fatalf("resource metadata=%+v", item)
		}
		if item.ID != 0 || item.Version != 0 || !item.CreatedAt.IsZero() || !item.UpdatedAt.IsZero() {
			t.Fatal("adapter assigned store-owned fields")
		}
		if item.Summary["provider_type"] != nativeType {
			t.Fatalf("native type=%v", item.Summary)
		}
		tags, _ := item.Details["tags"].(map[string]string)
		if len(tags) != len(item.Tags) {
			t.Fatalf("tags=%v details tags=%v", item.Tags, tags)
		}
		for key, value := range tags {
			if item.Tags[key] != value {
				t.Fatal("tag lost")
			}
		}
		actual = append(actual, cloud.Instance{ID: item.ProviderID, Name: item.Name, State: item.State, Type: item.Summary["instance_type"].(string), PublicIP: item.Summary["public_ip"].(string), PrivateIP: item.Summary["private_ip"].(string), Region: item.Region, Details: item.Details})
	}
	for i := range expected {
		if specs[0].Regional {
			expected[i].Region = account.Region
		}
	}
	sortInstances := func(items []cloud.Instance) {
		slices.SortFunc(items, func(a, b cloud.Instance) int { return strings.Compare(a.ID, b.ID) })
	}
	sortInstances(actual)
	sortInstances(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("adapter differs from legacy:\ngot %#v\nwant %#v", actual, expected)
	}

	replay.Reset(false, false)
	found, err := adapter.Get(ctx, account, providers.Instance, resources[0].ProviderID)
	if err != nil || found.ProviderID != resources[0].ProviderID {
		t.Fatalf("Get=%+v err=%v", found, err)
	}
	found.SeenAt = resources[0].SeenAt
	if !reflect.DeepEqual(found, resources[0]) {
		t.Fatalf("Get mapping differs from List: %+v", found)
	}
	replay.Reset(false, false)
	_, err = adapter.Get(ctx, account, providers.Instance, "missing")
	if !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("missing Get=%v", err)
	}
	for _, fail := range []bool{false, true} {
		replay.Reset(fail, !fail)
		items, rels, err := adapter.List(ctx, account, providers.Compute, account.Region)
		if fail {
			if err == nil || items != nil || rels != nil {
				t.Fatalf("partial error result: %v %v %v", items, rels, err)
			}
			replay.AssertPages(2)
			replay.Reset(true, false)
			if item, getErr := adapter.Get(ctx, account, providers.Instance, resources[0].ProviderID); getErr == nil || item.ProviderID != "" {
				t.Fatalf("Get returned an item from incomplete inventory: %+v %v", item, getErr)
			}
			replay.Reset(true, false)
			old, err := legacy()
			if err == nil || old != nil {
				t.Fatal("legacy error fixture did not fail atomically")
			}
		} else if err != nil || len(items) != 0 || len(rels) != 0 {
			t.Fatalf("empty result: %v %v", items, err)
		}
	}
	replay.Reset(false, false)
	_, _, err = adapter.List(ctx, account, "network", account.Region)
	if !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal("unsupported service accepted")
	}
	_, err = adapter.Get(ctx, account, "network", "id")
	if !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal("unsupported kind accepted")
	}
	invalid := account
	invalid.WorkspaceID = 0
	if _, _, err = adapter.List(ctx, invalid, providers.Compute, account.Region); err == nil {
		t.Fatal("missing workspace accepted")
	}
	invalid = account
	invalid.ID = 0
	if _, _, err = adapter.List(ctx, invalid, providers.Compute, account.Region); err == nil {
		t.Fatal("missing account accepted")
	}
	invalid = account
	invalid.Provider = "other"
	if _, _, err = adapter.List(ctx, invalid, providers.Compute, account.Region); err == nil {
		t.Fatal("wrong provider accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = adapter.List(cancelled, account, providers.Compute, account.Region); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	replay.AssertPages(0)
	if specs[0].Regional {
		if _, _, err = adapter.List(ctx, account, providers.Compute, ""); err == nil {
			t.Fatal("missing region accepted")
		}
	} else {
		replay.Reset(false, false)
		subset, _, err := adapter.List(ctx, account, providers.Compute, resources[0].Region)
		if err != nil || len(subset) != 1 || subset[0].Region != resources[0].Region {
			t.Fatalf("region filtering: %v %v", subset, err)
		}
	}
}
