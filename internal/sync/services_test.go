// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/jobs"
	syncer "github.com/netriun/nexus/internal/sync"
	"github.com/netriun/nexus/internal/worker"
)

var serviceScopes = []struct{ service, region string }{
	{syncer.Desktops, "cn-hangzhou"}, {syncer.Users, "cn-shanghai"},
	{syncer.Buckets, "*"}, {syncer.SecurityGroups, "cn-hangzhou"},
}

func serviceState(t *testing.T, s *syncer.Store, account int64, service, region string) {
	t.Helper()
	if _, err := s.DB.Exec(t.Context(), `UPDATE cloud_accounts SET provider='alibaba' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO sync_state(account_id,service,region,"interval") VALUES($1,$2,$3,interval '5 minutes')`, account, service, region); err != nil {
		t.Fatal(err)
	}
}
func serviceWrite(s *syncer.Store, value string) syncer.Collector {
	return func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
		return false, s.SaveServiceSnapshot(ctx, p.WorkspaceID, p.AccountID, p.Service, p.Region, map[string]any{"data": []map[string]string{{"id": value, "region": p.Region}}})
	}
}
func TestServiceManualDurabilityDedupeAndOwnership(t *testing.T) {
	for _, scope := range serviceScopes {
		t.Run(scope.service, func(t *testing.T) {
			s, workspace, account := setup(t)
			serviceState(t, s, account, scope.service, scope.region)
			if _, err := s.EnqueueDue(t.Context()); err != nil {
				t.Fatal(err)
			}
			receipt, err := s.RefreshService(t.Context(), workspace, []int64{account}, scope.service, scope.region)
			if err != nil {
				t.Fatal(err)
			}
			var count, priority int
			if err := s.DB.QueryRow(t.Context(), `SELECT count(*),max(priority) FROM jobs`).Scan(&count, &priority); err != nil || count != 1 || priority != 100 {
				t.Fatalf("dedupe/promotion: %d %d %v", count, priority, err)
			}
			// Replace all request-side state; receipts and executable work remain in SQL.
			s = syncer.New(s.DB, s.Config)
			if _, err := s.ServiceRefreshStatus(t.Context(), workspace+1, receipt.ID, []int64{account}, scope.service); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("foreign workspace: %v", err)
			}
			if _, err := s.ServiceRefreshStatus(t.Context(), workspace, receipt.ID, nil, scope.service); !errors.Is(err, syncer.ErrAccountAccess) {
				t.Fatalf("account access: %v", err)
			}
			j, err := claimReady(t, s, "replacement", time.Minute, syncer.JobKind)
			if err != nil || j == nil {
				t.Fatalf("claim: %v", err)
			}
			if j.ProviderKey != "provider:alibaba" || j.ConcurrencyKey != fmt.Sprintf("account:%d", account) {
				t.Fatalf("limits: %+v", j)
			}
			joined, err := s.RefreshService(t.Context(), workspace, []int64{account}, scope.service, scope.region)
			if err != nil || joined.ID != receipt.ID {
				t.Fatalf("running join: %+v %v", joined, err)
			}
			if err := s.Handler(serviceWrite(s, "fresh"))(t.Context(), *j); err != nil {
				t.Fatal(err)
			}
			if err := s.Queue.Complete(t.Context(), *j); err != nil {
				t.Fatal(err)
			}
			status, err := s.ServiceRefreshStatus(t.Context(), workspace, receipt.ID, []int64{account}, scope.service)
			if err != nil || status.Status != "completed" {
				t.Fatalf("completion: %+v %v", status, err)
			}
			// Even a valid receipt cannot expose a job whose payload belongs elsewhere.
			if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET payload=jsonb_set(payload,'{workspace_id}',to_jsonb($2::bigint)) WHERE id=$1`, j.ID, workspace+1); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ServiceRefreshStatus(t.Context(), workspace, receipt.ID, []int64{account}, scope.service); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("payload isolation: %v", err)
			}
		})
	}
}
func TestServiceLeaseFencingAndFailurePreservesSnapshot(t *testing.T) {
	for _, scope := range serviceScopes {
		t.Run(scope.service, func(t *testing.T) {
			s, workspace, account := setup(t)
			serviceState(t, s, account, scope.service, scope.region)
			old := queued(t, s)
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- s.Handler(func(ctx context.Context, p syncer.Payload, a syncer.Account) (bool, error) {
					close(started)
					<-release
					return serviceWrite(s, "stale")(ctx, p, a)
				})(t.Context(), *old)
			}()
			<-started
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET locked_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
				t.Fatal(err)
			}
			current, err := claimReady(t, s, "new", time.Minute, syncer.JobKind)
			if err != nil || current == nil {
				t.Fatalf("reclaim: %v", err)
			}
			if err := s.Handler(serviceWrite(s, "fresh"))(t.Context(), *current); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err == nil {
				t.Fatal("stale write accepted")
			}
			if err := s.Queue.Complete(t.Context(), *current); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RefreshService(t.Context(), workspace, []int64{account}, scope.service, scope.region); err != nil {
				t.Fatal(err)
			}
			failed, err := claimReady(t, s, "failed", time.Minute, syncer.JobKind)
			if err != nil || failed == nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(t.Context(), `UPDATE sync_state SET empty=(region<>'*') WHERE account_id=$1`, account); err != nil {
				t.Fatal(err)
			}
			err = s.Handler(func(ctx context.Context, p syncer.Payload, a syncer.Account) (bool, error) { return false, authError{} })(t.Context(), *failed)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe failure: %v", err)
			}
			var value, code string
			var empty, success bool
			if err := s.DB.QueryRow(t.Context(), `SELECT ss.payload->'data'->0->>'id',st.last_error_code,st.empty,st.last_success_at IS NOT NULL FROM sync_state st JOIN service_snapshots ss ON ss.account_id=st.account_id AND ss.service_key=st.service AND ss.region=st.region WHERE st.account_id=$1`, account).Scan(&value, &code, &empty, &success); err != nil {
				t.Fatal(err)
			}
			if value != "fresh" || code != "NX-SYNC-AUTH" || empty || !success {
				t.Fatalf("failure lost data/history: %s %s %v %v", value, code, empty, success)
			}
			// No caller can write snapshots without a lease-bearing handler context.
			if err := s.SaveServiceSnapshot(t.Context(), workspace, account, scope.service, scope.region, map[string]any{}); !errors.Is(err, jobs.ErrLeaseLost) {
				t.Fatalf("unfenced write: %v", err)
			}
		})
	}
}
func TestServiceSlowAccountFairness(t *testing.T) {
	for _, scope := range serviceScopes {
		t.Run(scope.service, func(t *testing.T) {
			s, workspace, slow := setup(t)
			serviceState(t, s, slow, scope.service, scope.region)
			var fast int64
			if err := s.DB.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'Fast','alibaba','test',ARRAY['cn-hangzhou']) RETURNING id`, workspace).Scan(&fast); err != nil {
				t.Fatal(err)
			}
			serviceState(t, s, fast, scope.service, scope.region)
			for _, account := range []int64{slow, fast} {
				if _, err := s.RefreshService(t.Context(), workspace, []int64{account}, scope.service, scope.region); err != nil {
					t.Fatal(err)
				}
			}
			q, err := jobs.New(s.DB, map[string]int{"account:*": 1, "provider:*": 2})
			if err != nil {
				t.Fatal(err)
			}
			started, finished, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			handler := s.Handler(func(ctx context.Context, p syncer.Payload, a syncer.Account) (bool, error) {
				if p.AccountID == slow {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return false, ctx.Err()
					}
				} else {
					<-started
					close(finished)
				}
				return serviceWrite(s, "fresh")(ctx, p, a)
			})
			cfg := worker.DefaultConfig()
			cfg.Concurrency = 2
			cfg.PollInterval = 10 * time.Millisecond
			w, err := worker.New(q, worker.Registry{syncer.JobKind: handler}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			defer func() {
				close(release)
				stop()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("slow account blocked fast account")
			}
		})
	}
}

func TestServiceRegionalUpgradePreservesHistory(t *testing.T) {
	s, workspace, account := setup(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	if _, err := s.DB.Exec(t.Context(), `UPDATE cloud_accounts SET provider='alibaba',regions=ARRAY['cn-hangzhou','ap-southeast-1'] WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{syncer.Users, syncer.SecurityGroups} {
		if _, err := s.DB.Exec(t.Context(), `INSERT INTO sync_state(account_id,service,region,"interval",last_success_at) VALUES($1,$2,'*',interval '5 minutes',$3)`, account, service, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct{ service, region, payload string }{
		{syncer.SecurityGroups, "", `{"data":[{"id":"sg-cn","region":"cn-hangzhou"},{"id":"sg-int","region":"ap-southeast-1"}],"failed_regions":[]}`},
		{syncer.Users, "cn-hangzhou", `{"data":[{"EndUserId":"mainland-user"}]}`},
		{syncer.Desktops, "all", `{"data":[{"DesktopId":"ecd-old","RegionId":"cn-hangzhou"}],"failed_regions":[]}`},
	} {
		if _, err := s.DB.Exec(t.Context(), `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload,fetched_at) VALUES($1,$2,$3,$4,$5,$6)`, workspace, account, fixture.service, fixture.region, fixture.payload, old); err != nil {
			t.Fatal(err)
		}
	}
	// Database-only reconciliation also upgrades explicitly selected accounts.
	if err := s.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ service, region string }{{syncer.Users, "cn-shanghai"}, {syncer.SecurityGroups, "cn-hangzhou"}, {syncer.SecurityGroups, "ap-southeast-1"}} {
		var success time.Time
		if err := s.DB.QueryRow(t.Context(), `SELECT last_success_at FROM sync_state WHERE account_id=$1 AND service=$2 AND region=$3`, account, scope.service, scope.region).Scan(&success); err != nil || !success.Equal(old) {
			t.Fatalf("lost history %v: %v %v", scope, success, err)
		}
	}
	var never bool
	if err := s.DB.QueryRow(t.Context(), `SELECT last_success_at IS NULL FROM sync_state WHERE account_id=$1 AND service='eds.users' AND region='ap-southeast-1'`, account).Scan(&never); err != nil || !never {
		t.Fatalf("invented international directory history: %v", err)
	}
	current := scopes(t, s, account)
	if current["eds.users:*"] != 0 || current["ecs.security_groups:*"] != 0 {
		t.Fatalf("legacy scopes remain: %v", current)
	}
	snapshot, exists, err := s.ServiceSnapshot(t.Context(), workspace, account, syncer.SecurityGroups, "")
	if err != nil || !exists || len(snapshot["data"].([]json.RawMessage)) != 2 {
		t.Fatalf("aggregate lost old groups: %+v %v", snapshot, err)
	}
	if err := s.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Disabling collection must not hide a previously successful snapshot.
	if _, err := s.DB.Exec(t.Context(), `UPDATE sync_state SET enabled=false WHERE account_id=$1 AND service=$2 AND region='cn-hangzhou'`, account, syncer.SecurityGroups); err != nil {
		t.Fatal(err)
	}
	snapshot, exists, err = s.ServiceSnapshot(t.Context(), workspace, account, syncer.SecurityGroups, "")
	if err != nil || !exists || len(snapshot["data"].([]json.RawMessage)) != 2 {
		t.Fatalf("disabled scope hid data: %+v %v", snapshot, err)
	}
	// Region edits preserve OSS identity and a directory shared by both CN regions.
	changeAccount(t, s, account, "alibaba", []string{"cn-beijing"})
	after := scopes(t, s, account)
	for _, key := range []string{"oss.buckets:*", "eds.users:cn-shanghai"} {
		if after[key] != current[key] {
			t.Fatalf("shared scope changed: %s", key)
		}
	}
	if after["ecs.security_groups:cn-beijing"] == 0 || after["ecs.security_groups:cn-hangzhou"] != 0 {
		t.Fatalf("regional selection: %v", after)
	}
}

func TestServiceMigrationWaitsForDiscovery(t *testing.T) {
	s, _, account := setup(t)
	if _, err := s.DB.Exec(t.Context(), `UPDATE cloud_accounts SET provider='alibaba',regions='{}' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	serviceState(t, s, account, syncer.SecurityGroups, "*")
	if err := s.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if scopes(t, s, account)["ecs.security_groups:*"] == 0 {
		t.Fatal("history removed before discovery")
	}
	if _, err := s.EnqueueDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE payload->>'service'='ecs.security_groups'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy wildcard executed: %d %v", n, err)
	}
	if err := s.Reconcile(t.Context(), func(context.Context, syncer.Account) map[string][]string {
		return map[string][]string{syncer.Compute: {"cn-hangzhou"}, syncer.Desktops: {"cn-hangzhou"}}
	}); err != nil {
		t.Fatal(err)
	}
	if got := scopes(t, s, account); got["ecs.security_groups:*"] != 0 || got["ecs.security_groups:cn-hangzhou"] == 0 {
		t.Fatalf("discovery failed migration: %v", got)
	}
}

func TestServiceCancellationRecordsWithFreshContext(t *testing.T) {
	for _, scope := range serviceScopes {
		t.Run(scope.service, func(t *testing.T) {
			s, _, account := setup(t)
			serviceState(t, s, account, scope.service, scope.region)
			j := queued(t, s)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := s.Handler(func(ctx context.Context, _ syncer.Payload, _ syncer.Account) (bool, error) {
				cancel()
				return false, ctx.Err()
			})(ctx, *j)
			if err == nil {
				t.Fatal("cancellation reported success")
			}
			var code string
			var empty, started bool
			if err := s.DB.QueryRow(t.Context(), `SELECT last_error_code,empty,last_started_at IS NOT NULL FROM sync_state WHERE account_id=$1`, account).Scan(&code, &empty, &started); err != nil {
				t.Fatal(err)
			}
			if code != "NX-SYNC-CANCELLED" || empty || !started {
				t.Fatalf("cancelled context lost status: %s %v %v", code, empty, started)
			}
		})
	}
}

func TestUpgradeUsesKnownRegionsBeforeNextDiscovery(t *testing.T) {
	s, workspace, account := setup(t)
	if _, err := s.DB.Exec(t.Context(), `UPDATE cloud_accounts SET provider='alibaba',regions='{}' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ service, region string }{{syncer.Compute, "cn-hangzhou"}, {syncer.Desktops, "cn-hangzhou"}, {syncer.Users, "*"}, {syncer.SecurityGroups, "*"}} {
		serviceState(t, s, account, scope.service, scope.region)
	}
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload) VALUES($1,$2,'eds.desktops','all','{"data":[{"DesktopId":"old","RegionId":"cn-hangzhou"}],"failed_regions":[]}')`, workspace, account); err != nil {
		t.Fatal(err)
	}
	// Reads must preserve aggregate-only historical rows even before reconcile.
	snapshot, exists, err := s.ServiceSnapshot(t.Context(), workspace, account, syncer.Desktops, "all")
	if err != nil || !exists || len(snapshot["data"].([]json.RawMessage)) != 1 {
		t.Fatalf("legacy data unavailable: %+v %v", snapshot, err)
	}
	if err := s.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	got := scopes(t, s, account)
	if got["eds.users:*"] != 0 || got["ecs.security_groups:*"] != 0 || got["eds.users:cn-shanghai"] == 0 || got["ecs.security_groups:cn-hangzhou"] == 0 {
		t.Fatalf("known scopes not reused: %v", got)
	}
}
