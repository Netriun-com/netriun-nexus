// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
	"github.com/netriun/nexus/internal/providers/aws"
	"github.com/netriun/nexus/internal/providers/azure"
	"github.com/netriun/nexus/internal/providers/gcp"
	syncer "github.com/netriun/nexus/internal/sync"
)

// Replay T18's API fixtures through actual adapters and the scheduled handler.
// Every HTTP operation, including OAuth, stays in memory and fails closed.
type computeReplay struct {
	t           *testing.T
	provider    string
	mu          sync.Mutex
	pages       int
	fail, empty bool
	cancel      context.CancelFunc
}

func (r *computeReplay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, body, contentType := 200, "", "application/json"
	if req.URL.Host == "login.microsoftonline.com" || req.URL.Host == "oauth2.googleapis.com" {
		body = `{"access_token":"fixture","token_type":"Bearer","expires_in":3600}`
	} else {
		name := ""
		if r.provider == "azure" && strings.HasSuffix(req.URL.Path, "/instanceView") {
			name = "view.json"
		} else {
			token := ""
			switch r.provider {
			case "aws":
				if req.URL.Host != "ec2.us-east-1.amazonaws.com" {
					return nil, fmt.Errorf("unexpected AWS request: %s", req.URL)
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
					return nil, fmt.Errorf("unexpected AWS action")
				}
				token = form.Get("NextToken")
				contentType = "text/xml"
			case "alibaba":
				if req.URL.Host != "ecs-cn-hangzhou.aliyuncs.com" {
					return nil, fmt.Errorf("unexpected Alibaba request: %s", req.URL)
				}
				token = req.URL.Query().Get("NextToken")
			case "azure":
				if req.URL.Host != "management.azure.com" || !strings.HasSuffix(req.URL.Path, "/virtualMachines") {
					return nil, fmt.Errorf("unexpected Azure request: %s", req.URL)
				}
				token = req.URL.Query().Get("$skiptoken")
			case "gcp":
				if req.URL.Host != "compute.googleapis.com" || !strings.HasSuffix(req.URL.Path, "/aggregated/instances") {
					return nil, fmt.Errorf("unexpected GCP request: %s", req.URL)
				}
				token = req.URL.Query().Get("pageToken")
			}
			expected := ""
			if r.pages == 1 {
				expected = "page-two"
			}
			if token != expected || r.pages > 1 {
				return nil, fmt.Errorf("invalid pagination token %q on page %d", token, r.pages)
			}
			r.pages++
			ext := ".json"
			if r.provider == "aws" {
				ext = ".xml"
			}
			name = fmt.Sprintf("page%d%s", r.pages, ext)
			if r.empty {
				name = "empty" + ext
			}
			if r.pages == 2 {
				if r.cancel != nil {
					r.cancel()
					return nil, context.Canceled
				}
				if r.fail {
					name = "error" + ext
					status = 403
				}
			}
		}
		data, err := os.ReadFile("../providers/" + r.provider + "/testdata/" + name)
		if err != nil {
			return nil, err
		}
		body = string(data)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
func (r *computeReplay) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	return r.RoundTrip(req)
}
func (r *computeReplay) reset(fail, empty bool, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages = 0
	r.fail = fail
	r.empty = empty
	r.cancel = cancel
}

func computeTestApp(t *testing.T, provider, region string) (*App, int64, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for compute dual-write integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Database != "ccmp_test" {
		t.Fatal("use isolated ccmp_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{fmt.Sprintf("compute_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	a, err := NewCollector(t.Context(), db, syncer.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	// Startup builds all four adapters exactly once, without loading credentials.
	for _, name := range []string{"aws", "alibaba", "azure", "gcp"} {
		if _, err := a.Providers.Lookup(name); err != nil {
			t.Fatal(err)
		}
	}
	var workspace, account int64
	if err := db.QueryRow(t.Context(), `INSERT INTO workspaces(name,slug) VALUES('Compute','compute') RETURNING id`).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	regions := []string{}
	if region != "*" {
		regions = []string{region, "other-region"}
	}
	if err := db.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'Compute',$2,'placeholder',$3) RETURNING id`, workspace, provider, regions).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	return a, workspace, account
}

func recordedComputeRegistry(t *testing.T, provider string, replay *computeReplay) *providers.Registry {
	t.Helper()
	ctx := t.Context()
	var adapter providers.Provider
	var err error
	switch provider {
	case "aws":
		client := ec2.NewFromConfig(sdkaws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, HTTPClient: &http.Client{Transport: replay}})
		adapter, err = aws.New(func(context.Context, providers.Account, string) (*ec2.Client, error) { return client, nil })
	case "alibaba":
		client, e := cloud.AlibabaClient("cn-hangzhou", "fixture", "fixture", "")
		if e != nil {
			t.Fatal(e)
		}
		client.HttpClient = replay
		adapter, err = alibaba.New(func(context.Context, providers.Account, string) (*ecs.Client, error) { return client, nil })
	case "azure", "gcp":
		original := http.DefaultTransport
		http.DefaultTransport = replay
		t.Cleanup(func() { http.DefaultTransport = original })
		if provider == "azure" {
			client, e := cloud.NewAzureClient(ctx, "tenant", "fixture", "fixture", "sub")
			if e != nil {
				t.Fatal(e)
			}
			adapter, err = azure.New(func(context.Context, providers.Account) (*cloud.AzureClient, error) { return client, nil })
		} else {
			key, e := rsa.GenerateKey(rand.Reader, 2048)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := json.Marshal(map[string]string{"client_email": "fixture@example.invalid", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), "token_uri": "https://oauth2.googleapis.com/token"})
			if e != nil {
				t.Fatal(e)
			}
			client, e := cloud.NewGCPClient(ctx, "project", string(raw))
			if e != nil {
				t.Fatal(e)
			}
			adapter, err = gcp.New(func(context.Context, providers.Account) (*cloud.GCPClient, error) { return client, nil })
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	registry, err := providers.NewRegistry(map[string]providers.Provider{provider: adapter})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func runComputeTest(t *testing.T, a *App, ctx context.Context, workspace, account int64, region string) error {
	t.Helper()
	if _, err := a.Sync.RefreshCompute(t.Context(), workspace, []int64{account}, region); err != nil {
		t.Fatal(err)
	}
	job, err := a.Sync.Queue.Claim(t.Context(), "compute-test", time.Minute, syncer.JobKind)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	err = a.Sync.Handler(a.CollectScheduled)(ctx, *job)
	// Complete the attempt even in failure cases so the next explicit test refresh
	// is independent of queue retry policy (which is covered by queue tests).
	if e := a.Sync.Queue.Complete(t.Context(), *job); e != nil {
		t.Fatal(e)
	}
	return err
}

func inventorySnapshot(t *testing.T, a *App, account int64) string {
	t.Helper()
	var raw string
	err := a.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'instances',(SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY region,instance_id),'[]') FROM instances i WHERE account_id=$1),
 'resources',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY service,kind,region,provider_id),'[]') FROM resources r WHERE account_id=$1))::text`, account).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertComputeParity(t *testing.T, a *App, workspace, account int64, count int) {
	t.Helper()
	var n, m int
	var same bool
	err := a.DB.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM instances WHERE account_id=$1),
 (SELECT count(*) FROM resources WHERE account_id=$1 AND service='compute' AND kind='instance'),
 NOT EXISTS(SELECT 1 FROM (SELECT * FROM instances WHERE account_id=$1) i FULL JOIN
 (SELECT * FROM resources WHERE workspace_id=$2 AND account_id=$1 AND service='compute' AND kind='instance') r
 ON i.region=r.region AND i.instance_id=r.provider_id
 WHERE i.id IS NULL OR r.id IS NULL OR (i.name,i.state,i.instance_type,i.public_ip,i.private_ip,i.details)
 IS DISTINCT FROM (r.name,r.state,r.summary->>'instance_type',r.summary->>'public_ip',r.summary->>'private_ip',r.details)
 OR r.tags IS DISTINCT FROM coalesce(r.details->'tags','{}'::jsonb) OR r.summary->>'provider_type' IS NULL)`, account, workspace).Scan(&n, &m, &same)
	if err != nil || n != count || m != count || !same {
		t.Fatalf("dual-write parity: instances=%d resources=%d same=%v err=%v", n, m, same, err)
	}
}

func TestComputeRegistryDualWriteRecordedResponses(t *testing.T) {
	for _, tc := range []struct{ provider, region string }{{"aws", "us-east-1"}, {"alibaba", "cn-hangzhou"}, {"azure", "*"}, {"gcp", "*"}} {
		t.Run(tc.provider, func(t *testing.T) {
			a, workspace, account := computeTestApp(t, tc.provider, tc.region)
			replay := &computeReplay{t: t, provider: tc.provider}
			a.Providers = recordedComputeRegistry(t, tc.provider, replay)
			// Seed unseen resources, including an entirely disappeared global region.
			oldRegion := tc.region
			if oldRegion == "*" {
				oldRegion = "gone-region"
			}
			if _, err := a.DB.Exec(t.Context(), `WITH old_instance AS (INSERT INTO instances(account_id,region,instance_id,state,instance_type) VALUES($1,$2,'old','running','small') RETURNING id) INSERT INTO resources(workspace_id,account_id,provider,service,kind,region,provider_id) VALUES($3,$1,$4,'compute','instance',$2,'old')`, account, oldRegion, workspace, tc.provider); err != nil {
				t.Fatal(err)
			}
			if err := runComputeTest(t, a, t.Context(), workspace, account, tc.region); err != nil {
				t.Fatal(err)
			}
			assertComputeParity(t, a, workspace, account, 2)
			if replay.pages != 2 {
				t.Fatalf("pages=%d", replay.pages)
			}
			replay.reset(false, false, nil)
			if err := runComputeTest(t, a, t.Context(), workspace, account, tc.region); err != nil {
				t.Fatal(err)
			}
			var stable bool
			if err := a.DB.QueryRow(t.Context(), `SELECT bool_and(version=1) FROM resources WHERE account_id=$1`, account).Scan(&stable); err != nil || !stable {
				t.Fatalf("unchanged sync bumped version: %v %v", stable, err)
			}
			for _, mode := range []string{"error-page", "cancelled-page"} {
				before := inventorySnapshot(t, a, account)
				ctx, cancel := context.WithCancel(t.Context())
				if mode == "error-page" {
					replay.reset(true, false, nil)
				} else {
					replay.reset(false, false, cancel)
				}
				err := runComputeTest(t, a, ctx, workspace, account, tc.region)
				cancel()
				if err == nil {
					t.Fatalf("%s succeeded", mode)
				}
				if after := inventorySnapshot(t, a, account); after != before {
					t.Fatalf("%s changed inventory", mode)
				}
			}
			if tc.region == "*" {
				// Filter to one configured region. The other previously stored region must
				// be removed from both tables even though it is absent after filtering.
				var selected string
				if err := a.DB.QueryRow(t.Context(), `SELECT min(region) FROM instances WHERE account_id=$1`, account).Scan(&selected); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(t.Context(), `UPDATE cloud_accounts SET regions=ARRAY[$2] WHERE id=$1`, account, selected); err != nil {
					t.Fatal(err)
				}
				replay.reset(false, false, nil)
				if err := runComputeTest(t, a, t.Context(), workspace, account, tc.region); err != nil {
					t.Fatal(err)
				}
				assertComputeParity(t, a, workspace, account, 1)
			}
			replay.reset(false, true, nil)
			if err := runComputeTest(t, a, t.Context(), workspace, account, tc.region); err != nil {
				t.Fatal(err)
			}
			assertComputeParity(t, a, workspace, account, 0)
		})
	}
}
