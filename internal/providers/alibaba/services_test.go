// SPDX-License-Identifier: AGPL-3.0-only

package alibaba_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
	"github.com/netriun/nexus/internal/providers/alibaba/alibabatest"
)

func legacyService(t *testing.T, replay *alibabatest.Replay) any {
	t.Helper()
	ctx := t.Context()
	account := replay.Account
	region := replay.Region
	switch replay.Service {
	case providers.EDSDesktops:
		c, err := replay.EDS(ctx, account, region)
		if err != nil {
			t.Fatal(err)
		}
		items, err := cloud.EDSDesktops(ctx, c.Desktop, region)
		if err != nil {
			t.Fatal(err)
		}
		return items
	case providers.EDSUsers:
		c, err := replay.EDS(ctx, account, cloud.EDSUserRegion(region))
		if err != nil {
			t.Fatal(err)
		}
		items, err := cloud.EDSUsers(ctx, c.User)
		if err != nil {
			t.Fatal(err)
		}
		return items
	case providers.OSSBuckets:
		c, err := replay.OSS(ctx, account, "*")
		if err != nil {
			t.Fatal(err)
		}
		items, err := cloud.OSSListBuckets(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		for i := range items {
			c, err := replay.OSS(ctx, account, items[i].Region)
			if err != nil {
				t.Fatal(err)
			}
			if err := cloud.OSSBucketDetails(ctx, c, &items[i]); err != nil {
				t.Fatal(err)
			}
			if err := cloud.OSSBucketStats(ctx, c, &items[i]); err != nil {
				t.Fatal(err)
			}
			items[i].Status = "available"
		}
		return items
	case providers.SecurityGroups:
		c, err := replay.ECS(ctx, account, region)
		if err != nil {
			t.Fatal(err)
		}
		items, err := cloud.AlibabaSecurityGroups(ctx, c, region)
		if err != nil {
			t.Fatal(err)
		}
		for i := range items {
			rules, err := cloud.AlibabaSecurityGroupRules(ctx, c, region, items[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			items[i].Rules, items[i].RulesAvailable = rules, true
		}
		return items
	}
	t.Fatal("unknown fixture service")
	return nil
}

func TestRecordedServicesParity(t *testing.T) {
	for _, tc := range []struct{ service, kind, region, key, id, state string }{
		{providers.EDSDesktops, providers.Desktop, "cn-hangzhou", "desktops", "ecd-one", "running"},
		{providers.EDSUsers, providers.DirectoryUser, "cn-hangzhou", "users", "alice", "normal"},
		{providers.EDSUsers, providers.DirectoryUser, "cn-hongkong", "users", "alice", "normal"},
		{providers.OSSBuckets, providers.Bucket, "*", "buckets", "fixture-one", "available"},
		{providers.SecurityGroups, providers.SecurityGroup, "cn-hangzhou", "groups", "sg-one", "available"},
	} {
		t.Run(tc.service+"/"+tc.region, func(t *testing.T) {
			account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "alibaba", Region: tc.region}
			replay := alibabatest.New(account, tc.service, tc.region)
			adapter := replay.Adapter(t)
			specs := adapter.Services()
			if len(specs) != 5 {
				t.Fatalf("services: %+v", specs)
			}
			for i := range specs {
				if len(specs[i].Kinds) != 1 || specs[i].DefaultInterval <= 0 {
					t.Fatalf("invalid spec: %+v", specs[i])
				}
				specs[i].Kinds[0] = "mutated"
			}
			if adapter.Services()[1].Kinds[0] != providers.Desktop {
				t.Fatal("mutable service spec")
			}
			items, relations, err := adapter.List(t.Context(), account, tc.service, tc.region)
			if err != nil || len(items) != 2 || len(relations) != 0 {
				t.Fatalf("list: %+v %v", items, err)
			}
			if replay.Calls(tc.key) != 2 {
				t.Fatal("did not traverse both list pages")
			}
			if tc.service == providers.SecurityGroups && (replay.Calls("rules:sg-one") != 2 || replay.Calls("rules:sg-two") != 2) {
				t.Fatal("did not traverse both rule pages")
			}
			if tc.service == providers.OSSBuckets && (replay.Calls("info:fixture-two") != 1 || replay.Calls("stat:fixture-two") != 1) {
				t.Fatal("missing bucket details/stats")
			}
			expectedRegion := tc.region
			if tc.service == providers.EDSUsers {
				expectedRegion = cloud.EDSUserRegion(tc.region)
			}
			for _, item := range items {
				if item.WorkspaceID != 17 || item.AccountID != 42 || item.Provider != "alibaba" || item.Service != tc.service || item.Kind != tc.kind || item.SeenAt.IsZero() || !item.SeenAt.Equal(items[0].SeenAt) || item.Version != 0 || item.ID != 0 {
					t.Fatalf("metadata: %+v", item)
				}
				if tc.service != providers.OSSBuckets && item.Region != expectedRegion {
					t.Fatalf("wrong region: %s", item.Region)
				}
				if item.Summary["provider_type"] == nil {
					t.Fatal("missing provider type")
				}
				if tc.service == providers.SecurityGroups {
					if _, ok := item.Summary["rules"]; ok {
						t.Fatal("rules escaped into summary")
					}
				}
			}
			if items[0].ProviderID != tc.id || items[0].State != tc.state {
				t.Fatalf("identity/state: %+v", items[0])
			}
			if tc.service == providers.EDSDesktops && items[0].Tags["team"] != "engineering" {
				t.Fatal("desktop tags lost")
			}
			if tc.service == providers.EDSUsers && (items[1].State != "locked" || items[1].Name != "bob" || items[0].Details["Id"] != json.Number("9007199254740993")) {
				t.Fatal("user state, name fallback or numeric ID lost")
			}
			if tc.service == providers.OSSBuckets && (items[0].Region != "cn-hangzhou" || items[1].Region != "ap-southeast-1" || items[0].Details["storage_bytes"] != json.Number("9007199254740993")) {
				t.Fatal("bucket region or int64 counter lost")
			}
			replay.Reset("", nil)
			expected := legacyService(t, replay)
			raw, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			var objects []map[string]any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&objects); err != nil {
				t.Fatal(err)
			}
			for i, item := range items {
				if !reflect.DeepEqual(item.Details, objects[i]) {
					t.Fatalf("legacy snapshot differs: got %+v want %+v", item.Details, objects[i])
				}
			}
			replay.Reset("", nil)
			got, err := adapter.Get(t.Context(), account, tc.kind, tc.id)
			if err != nil || got.ProviderID != tc.id {
				t.Fatalf("Get: %+v %v", got, err)
			}
			replay.Reset("", nil)
			if _, err := adapter.Get(t.Context(), account, tc.kind, "missing"); !errors.Is(err, providers.ErrNotFound) {
				t.Fatalf("missing Get: %v", err)
			}
			modes := []string{"error-page", "cancelled-page"}
			if tc.service == providers.OSSBuckets {
				modes = append(modes, "info-error", "stat-error")
			}
			if tc.service == providers.SecurityGroups {
				modes = append(modes, "rules-error")
			}
			for _, mode := range modes {
				ctx, cancel := context.WithCancel(t.Context())
				callback := (func())(nil)
				if mode == "cancelled-page" {
					callback = cancel
				}
				replay.Reset(mode, callback)
				result, edges, err := adapter.List(ctx, account, tc.service, tc.region)
				cancel()
				if err == nil || result != nil || edges != nil {
					t.Fatalf("%s exposed partial result: %v %v", mode, result, err)
				}
				if mode == "cancelled-page" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			}
			replay.Reset("error-page", nil)
			if result, err := adapter.Get(t.Context(), account, tc.kind, tc.id); err == nil || result.ProviderID != "" {
				t.Fatal("Get exposed a partial collection")
			}
			replay.Reset("empty", nil)
			if result, edges, err := adapter.List(t.Context(), account, tc.service, tc.region); err != nil || len(result) != 0 || len(edges) != 0 {
				t.Fatalf("empty list: %+v %v", result, err)
			}
			replay.Reset("", nil)
			for _, bad := range []providers.Account{{ID: 42, Provider: "alibaba"}, {WorkspaceID: 17, Provider: "alibaba"}, {WorkspaceID: 17, ID: 42, Provider: "aws"}} {
				if _, _, err := adapter.List(t.Context(), bad, tc.service, tc.region); err == nil {
					t.Fatal("invalid identity accepted")
				}
			}
			badRegion := "*"
			if tc.service == providers.OSSBuckets {
				badRegion = "cn-hangzhou"
			}
			if _, _, err := adapter.List(t.Context(), account, tc.service, badRegion); err == nil {
				t.Fatal("invalid collection scope accepted")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := adapter.List(ctx, account, tc.service, tc.region); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled request accepted")
			}
			if replay.Calls(tc.key) != 0 {
				t.Fatal("invalid requests reached the provider")
			}
		})
	}
}

func TestServiceFactoryFailures(t *testing.T) {
	failure := errors.New("fixture factory failure")
	for _, failure := range []error{failure, nil} {
		adapter, err := alibaba.NewWithServices(
			func(context.Context, providers.Account, string) (*ecs.Client, error) { return nil, failure },
			func(context.Context, providers.Account, string) (*cloud.EDSClients, error) { return nil, failure },
			func(context.Context, providers.Account, string) (*oss.Client, error) { return nil, failure },
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, service := range []string{providers.EDSDesktops, providers.EDSUsers, providers.OSSBuckets, providers.SecurityGroups} {
			region := "cn-hangzhou"
			if service == providers.OSSBuckets {
				region = "*"
			}
			items, relations, err := adapter.List(t.Context(), providers.Account{WorkspaceID: 1, ID: 2, Provider: "alibaba"}, service, region)
			if err == nil || items != nil || relations != nil || (failure != nil && !errors.Is(err, failure)) {
				t.Fatalf("factory failure: %v %v", items, err)
			}
		}
	}
	if _, err := alibaba.NewWithServices(nil, nil, nil); err == nil {
		t.Fatal("missing factories accepted")
	}
}
