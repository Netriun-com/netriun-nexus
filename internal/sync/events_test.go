// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/providers"
	syncer "github.com/netriun/nexus/internal/sync"
)

func eventCount(t *testing.T, s *syncer.Store, workspace int64, pattern string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM resource_events WHERE workspace_id=$1 AND type LIKE $2`, workspace, pattern).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func clearEvents(t *testing.T, s *syncer.Store, workspace int64) {
	t.Helper()
	if _, err := s.DB.Exec(t.Context(), `DELETE FROM resource_events WHERE workspace_id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
}
func eventJob(t *testing.T, s *syncer.Store, workspace, account int64) *jobs.Job {
	t.Helper()
	if _, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	j, err := claimReady(t, s, "events", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %+v %v", j, err)
	}
	return j
}
func TestSyncResourceAndLifecycleEvents(t *testing.T) {
	s, workspace, account := setup(t)
	addState(t, s, account)
	makeItem := func(id string) providers.Resource { return computeResource(workspace, account, "aws", "us-east-1", id) }
	collect := func(items []providers.Resource) syncer.Collector {
		return func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
			return len(items) == 0, s.ReconcileInventory(ctx, account, items, nil, false, p.Region)
		}
	}
	j := eventJob(t, s, workspace, account)
	if err := s.Handler(collect([]providers.Resource{makeItem("same"), makeItem("change"), makeItem("gone")}))(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue.Complete(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	clearEvents(t, s, workspace)
	changed := makeItem("change")
	changed.State = "stopped"
	j = eventJob(t, s, workspace, account)
	if err := s.Handler(collect([]providers.Resource{makeItem("same"), changed, makeItem("new")}))(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if eventCount(t, s, workspace, "resource.%") != 3 || eventCount(t, s, workspace, "sync.%") != 2 {
		t.Fatal("wrong resource/lifecycle count")
	}
	var types string
	if err := s.DB.QueryRow(t.Context(), `SELECT string_agg(type,',' ORDER BY id) FROM resource_events WHERE workspace_id=$1`, workspace).Scan(&types); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(types, "sync.started,") || !strings.HasSuffix(types, ",sync.finished") {
		t.Fatalf("order: %s", types)
	}
	var valid bool
	if err := s.DB.QueryRow(t.Context(), `SELECT bool_and(account_id=$2 AND service='compute' AND region='us-east-1'
 AND resource_id IS NULL AND version IS NULL AND error_code='' AND
 ((type='sync.started' AND success IS NULL) OR (type='sync.finished' AND success)))
 FROM resource_events WHERE workspace_id=$1 AND type LIKE 'sync.%'`, workspace, account).Scan(&valid); err != nil || !valid {
		t.Fatalf("lifecycle payload invalid: %v %v", valid, err)
	}
}

func TestFailedSyncHasNoResourceEvents(t *testing.T) {
	for _, mode := range []string{"provider", "cancelled", "lease-lost", "lease-lost-after-write", "write-error"} {
		t.Run(mode, func(t *testing.T) {
			s, workspace, account := setup(t)
			addState(t, s, account)
			j := eventJob(t, s, workspace, account)
			if mode == "lease-lost-after-write" || mode == "write-error" {
				action := `UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE status='running';`
				if mode == "write-error" {
					action = `RAISE EXCEPTION 'provider-body-secret';`
				}
				// AFTER statement: catalog events have already been inserted. Final lease
				// check (or SQL failure) must roll those events back along with the catalog.
				if _, err := s.DB.Exec(t.Context(), `CREATE FUNCTION zz_event_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+action+` RETURN NULL; END $$;
  CREATE TRIGGER zz_event_failure AFTER INSERT ON resources FOR EACH STATEMENT EXECUTE FUNCTION zz_event_failure()`); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
				switch mode {
				case "provider":
					return false, errors.New("provider-body-secret credential-secret")
				case "cancelled":
					cancel()
					return false, ctx.Err()
				case "lease-lost":
					if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
						return false, err
					}
				}
				return false, s.ReconcileInventory(ctx, account, []providers.Resource{computeResource(workspace, account, "aws", "us-east-1", "new")}, nil, false, p.Region)
			})(ctx, *j)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if eventCount(t, s, workspace, "resource.%") != 0 {
				t.Fatal("failed collection emitted resource event")
			}
			var rows int
			if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM resources WHERE workspace_id=$1 AND account_id=$2`, workspace, account).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("partial catalog: %d %v", rows, err)
			}
			if eventCount(t, s, workspace, "sync.started") != 1 {
				t.Fatal("missing committed start")
			}
			if mode == "lease-lost" {
				if eventCount(t, s, workspace, "sync.finished") != 0 {
					t.Fatal("stale attempt emitted finish")
				}
				clearEvents(t, s, workspace)
				called := false
				if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { called = true; return true, nil })(t.Context(), *j); err != nil || called {
					t.Fatalf("stale start: %v called=%v", err, called)
				}
				if eventCount(t, s, workspace, "%") != 0 {
					t.Fatal("stale attempt emitted events")
				}
				return
			}
			var success bool
			var code string
			if err := s.DB.QueryRow(t.Context(), `SELECT success,error_code FROM resource_events WHERE workspace_id=$1 AND type='sync.finished'`, workspace).Scan(&success, &code); err != nil {
				t.Fatal(err)
			}
			expected := "NX-SYNC-PROVIDER"
			if mode == "cancelled" {
				expected = "NX-SYNC-CANCELLED"
			}
			if success || code != expected {
				t.Fatalf("unsafe/incorrect finish: %v %q", success, code)
			}
		})
	}
}

func TestSyncStartEventAtomic(t *testing.T) {
	s, workspace, account := setup(t)
	state := addState(t, s, account)
	j := eventJob(t, s, workspace, account)
	if _, err := s.DB.Exec(t.Context(), `CREATE FUNCTION fail_start_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$;
 CREATE TRIGGER fail_start_event BEFORE INSERT ON resource_events FOR EACH ROW EXECUTE FUNCTION fail_start_event()`); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { called = true; return true, nil })(t.Context(), *j); err == nil || called {
		t.Fatal("start failure ran collector")
	}
	var untouched bool
	if err := s.DB.QueryRow(t.Context(), `SELECT last_started_at IS NULL FROM sync_state WHERE id=$1`, state).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("state committed without event: %v %v", untouched, err)
	}
	if eventCount(t, s, workspace, "%") != 0 {
		t.Fatal("failed start emitted")
	}
}

func TestDailyEventRetention(t *testing.T) {
	s, workspace, account := setup(t)
	retentionSettings(t, s, workspace, 7)
	s.Config.EventRetention = 48 * time.Hour
	seed := func(workspace, account int64, age string, n int) {
		t.Helper()
		if _, err := s.DB.Exec(t.Context(), `INSERT INTO resource_events(workspace_id,account_id,type,service,region,created_at)
 SELECT $1,$2,'sync.started','compute','us-east-1',clock_timestamp()-$3::interval FROM generate_series(1,$4::int)`, workspace, account, age, n); err != nil {
			t.Fatal(err)
		}
	}
	seed(workspace, account, "49 hours", 1201)
	seed(workspace, account, "47 hours", 2)
	var other, otherAccount int64
	if err := s.DB.QueryRow(t.Context(), `INSERT INTO workspaces(name,slug) VALUES('Other','event-other') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials) VALUES($1,'Other','aws','placeholder') RETURNING id`, other).Scan(&otherAccount); err != nil {
		t.Fatal(err)
	}
	seed(other, otherAccount, "72 hours", 3)
	if _, err := s.EnqueueAuditRetention(t.Context()); err != nil {
		t.Fatal(err)
	}
	j, err := claimReady(t, s, "event-retention", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || j == nil {
		t.Fatalf("retention claim: %v %v", j, err)
	}
	// A stale daily worker cannot delete even the first batch.
	stale := *j
	stale.Attempts++
	if err := s.AuditRetentionHandler()(t.Context(), stale); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale retention: %v", err)
	}
	if eventCount(t, s, workspace, "%") != 1203 {
		t.Fatal("stale worker removed events")
	}
	if err := s.AuditRetentionHandler()(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if eventCount(t, s, workspace, "%") != 2 || eventCount(t, s, other, "%") != 3 {
		t.Fatal("retention crossed age/workspace boundary")
	}
	// Retrying an interrupted daily job is harmless.
	if err := s.AuditRetentionHandler()(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if eventCount(t, s, workspace, "%") != 2 {
		t.Fatal("retry removed recent events")
	}
	t.Log(fmt.Sprintf("removed 1201 old events in batches; retained 2 recent and 3 other-workspace events"))
}
