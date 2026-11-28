// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba/alibabatest"
	"github.com/netriun/nexus/internal/resources"
	syncer "github.com/netriun/nexus/internal/sync"
)

func runServiceResourcesTest(t *testing.T, a *App, ctx context.Context, workspace, account int64, service, region string) error {
	t.Helper()
	if _, err := a.Sync.RefreshService(t.Context(), workspace, []int64{account}, service, region); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := a.Sync.Queue.Claim(t.Context(), "service-resources-test", time.Minute, syncer.JobKind)
		if err != nil {
			t.Fatal(err)
		}
		if job != nil {
			result := a.Sync.Handler(a.CollectScheduled)(ctx, *job)
			// Finish each test attempt independently of queue retry tests.
			if err := a.Sync.Queue.Complete(t.Context(), *job); err != nil {
				t.Fatal(err)
			}
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("service job not claimed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func serviceResourcesSnapshot(t *testing.T, a *App, account int64) string {
	t.Helper()
	var raw string
	if err := a.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'snapshots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY service_key,region) FROM service_snapshots s WHERE account_id=$1),
 'resources',(SELECT jsonb_agg(to_jsonb(r) ORDER BY service,kind,region,provider_id) FROM resources r WHERE account_id=$1))::text`, account).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertServiceResourcesParity(t *testing.T, a *App, workspace, account int64, service, kind, region, idKey string, count int) {
	t.Helper()
	var identity bool
	var n int
	err := a.DB.QueryRow(t.Context(), `SELECT count(*),COALESCE(bool_and(provider='alibaba' AND provider_id=details->>$6 AND region=CASE WHEN $3='eds.users' THEN $4 ELSE COALESCE(details->>'RegionId',details->>'region') END),true) FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service=$3 AND kind=$5 AND ($4='*' OR region=$4)`, workspace, account, service, region, kind, idKey).Scan(&n, &identity)
	if err != nil || !identity || n != count {
		t.Fatalf("catalog identity=%v count=%d want=%d err=%v", identity, n, count, err)
	}
	payload, exists, err := a.Sync.ServiceSnapshot(t.Context(), workspace, account, service, region)
	if err != nil || !exists {
		t.Fatalf("snapshot read: %v %v", payload, err)
	}
	if service == syncer.Users {
		if _, ok := payload["failed_regions"]; ok {
			t.Fatal("users acquired failed_regions")
		}
	} else {
		field := "failed_regions"
		if service == syncer.Buckets {
			field = "failed_buckets"
		}
		if len(payload[field].([]string)) != 0 {
			t.Fatalf("snapshot shape: %v", payload)
		}
	}
	var legacy int
	if err := a.DB.QueryRow(t.Context(), `SELECT count(*) FROM service_snapshots WHERE account_id=$1`, account).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy writes: %d %v", legacy, err)
	}
}

func TestServiceResourcesRecordedCatalog(t *testing.T) {
	for _, tc := range []struct{ service, kind, region, idKey string }{
		{syncer.Desktops, providers.Desktop, "cn-hangzhou", "DesktopId"},
		{syncer.Users, providers.DirectoryUser, "cn-shanghai", "EndUserId"},
		{syncer.Buckets, providers.Bucket, "*", "name"},
		{syncer.SecurityGroups, providers.SecurityGroup, "cn-hangzhou", "id"},
	} {
		t.Run(tc.service, func(t *testing.T) {
			a, workspace, account := computeTestApp(t, "alibaba", "cn-hangzhou")
			identity := providers.Account{WorkspaceID: workspace, ID: account, Provider: "alibaba", Region: tc.region}
			replay := alibabatest.New(identity, tc.service, tc.region)
			registry, err := providers.NewRegistry(map[string]providers.Provider{"alibaba": replay.Adapter(t)})
			if err != nil {
				t.Fatal(err)
			}
			a.Providers = registry
			scope := resources.Scope{WorkspaceID: workspace, AccountID: account, Provider: "alibaba", Service: tc.service, Kind: tc.kind, Region: tc.region}
			if tc.service == syncer.Buckets {
				scope.Region = "gone-region"
			}
			seed := func(scope resources.Scope, id string) {
				t.Helper()
				tx, err := a.DB.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if err := resources.Reconcile(t.Context(), tx, scope, []resources.Item{{ProviderID: id, Name: id}}, nil); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			seed(scope, "old")
			preserved := scope
			preserved.Service = "other-service"
			preserved.Kind = "other-kind"
			seed(preserved, "keep")
			if tc.service != syncer.Buckets {
				preserved = scope
				preserved.Region = "other-region"
				seed(preserved, "keep-region")
			}
			run := func(ctx context.Context) error {
				return runServiceResourcesTest(t, a, ctx, workspace, account, tc.service, tc.region)
			}
			if err := run(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertServiceResourcesParity(t, a, workspace, account, tc.service, tc.kind, tc.region, tc.idKey, 2)
			// OSS drops a disappeared real region and never creates a wildcard resource.
			var oldCount int
			if err := a.DB.QueryRow(t.Context(), `SELECT count(*) FROM resources WHERE account_id=$1 AND (provider_id='old' OR region='*')`, account).Scan(&oldCount); err != nil || oldCount != 0 {
				t.Fatalf("old/wildcard resources: %d %v", oldCount, err)
			}
			replay.Reset("", nil)
			if err := run(t.Context()); err != nil {
				t.Fatal(err)
			}
			var stable bool
			if err := a.DB.QueryRow(t.Context(), `SELECT bool_and(version=1) FROM resources WHERE account_id=$1`, account).Scan(&stable); err != nil || !stable {
				t.Fatalf("unchanged sync bumped version: %v", err)
			}
			modes := []string{"error-page", "cancelled-page", "catalog-error", "expired-before-commit"}
			if tc.service == syncer.Buckets {
				modes = append(modes, "info-error", "stat-error")
			}
			if tc.service == syncer.SecurityGroups {
				modes = append(modes, "rules-error")
			}
			for _, mode := range modes {
				before := serviceResourcesSnapshot(t, a, account)
				ctx, cancel := context.WithCancel(t.Context())
				callback := (func())(nil)
				if mode == "cancelled-page" {
					callback = cancel
				}
				replay.Reset(mode, callback)
				if mode == "catalog-error" || mode == "expired-before-commit" {
					action := `RAISE EXCEPTION 'fixture catalog failure';`
					if mode == "expired-before-commit" {
						action = `UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE status='running';`
					}
					if _, err := a.DB.Exec(t.Context(), `CREATE FUNCTION fail_service_catalog() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+action+` RETURN NEW; END $$;
    CREATE TRIGGER fail_service_catalog BEFORE INSERT ON resources FOR EACH ROW EXECUTE FUNCTION fail_service_catalog()`); err != nil {
						t.Fatal(err)
					}
				}
				err := run(ctx)
				cancel()
				if mode == "catalog-error" || mode == "expired-before-commit" {
					if _, err := a.DB.Exec(t.Context(), `DROP TRIGGER fail_service_catalog ON resources; DROP FUNCTION fail_service_catalog()`); err != nil {
						t.Fatal(err)
					}
				}
				if err == nil {
					t.Fatalf("%s succeeded", mode)
				}
				if after := serviceResourcesSnapshot(t, a, account); after != before {
					t.Fatalf("%s changed a snapshot or resource", mode)
				}
			}
			replay.Reset("empty", nil)
			if err := run(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertServiceResourcesParity(t, a, workspace, account, tc.service, tc.kind, tc.region, tc.idKey, 0)
			var preservedCount int
			if err := a.DB.QueryRow(t.Context(), `SELECT count(*) FROM resources WHERE account_id=$1 AND provider_id LIKE 'keep%'`, account).Scan(&preservedCount); err != nil {
				t.Fatal(err)
			}
			expected := 2
			if tc.service == syncer.Buckets {
				expected = 1
			}
			if preservedCount != expected {
				t.Fatalf("cleared unrelated scopes: %d want %d", preservedCount, expected)
			}
			t.Logf("%s: recorded pages, exact JSON parity, retry, errors/cancellation, transactional lease fence and empty reconcile passed", tc.service)
		})
	}
}
