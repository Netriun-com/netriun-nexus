// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/app"
	"github.com/netriun/nexus/internal/jobs"
	syncer "github.com/netriun/nexus/internal/sync"
)

func setup(t *testing.T) (*syncer.Store, int64, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for scheduler integration tests")
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
	schema := fmt.Sprintf("sync_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
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
	if err := app.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var workspace, account int64
	if err := db.QueryRow(t.Context(), "INSERT INTO workspaces(name,slug) VALUES('Sync test','sync-test') RETURNING id").Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'Test','aws','encrypted-placeholder',ARRAY['us-east-1']) RETURNING id`, workspace).Scan(&account); err != nil {
		t.Fatal(err)
	}
	return syncer.New(db, syncer.DefaultConfig()), workspace, account
}

func addState(t *testing.T, s *syncer.Store, account int64) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRow(t.Context(), `INSERT INTO sync_state(account_id,service,region,"interval") VALUES($1,'compute','us-east-1',interval '5 minutes') RETURNING id`, account).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func queued(t *testing.T, s *syncer.Store) *jobs.Job {
	t.Helper()
	if _, err := s.EnqueueDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	j, err := s.Queue.Claim(t.Context(), "worker", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %+v %v", j, err)
	}
	return j
}

func TestConcurrentSchedulers(t *testing.T) {
	s, workspace, account := setup(t)
	addState(t, s, account)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			other := syncer.New(s.DB, s.Config)
			if _, err := other.EnqueueDue(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	var count int
	var key, concurrency string
	var payloadWorkspace int64
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*),min(dedupe_key),min(concurrency_key),min((payload->>'workspace_id')::bigint) FROM jobs`).Scan(&count, &key, &concurrency, &payloadWorkspace); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != fmt.Sprintf("sync:%d:compute:us-east-1", account) || concurrency != fmt.Sprintf("account:%d", account) || payloadWorkspace != workspace {
		t.Fatalf("jobs=%d key=%s concurrency=%s workspace=%d", count, key, concurrency, payloadWorkspace)
	}
	if n, err := s.EnqueueDue(t.Context()); err != nil || n != 0 {
		t.Fatalf("duplicated active schedule: %d %v", n, err)
	}
}

func TestEmptyAndFailedSync(t *testing.T) {
	for _, scenario := range []string{"empty", "account-wide-empty", "failure", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, account := setup(t)
			id := addState(t, s, account)
			if scenario == "account-wide-empty" {
				if _, err := s.DB.Exec(t.Context(), "UPDATE sync_state SET region='*' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			j := queued(t, s)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			handler := s.Handler(func(ctx context.Context, p syncer.Payload, a syncer.Account) (bool, error) {
				if p.AccountID != account || a.Provider != "aws" {
					t.Error("wrong collection scope")
				}
				switch scenario {
				case "empty", "account-wide-empty":
					return true, nil
				case "cancelled":
					cancel()
					return false, ctx.Err()
				default:
					return false, errors.New("secret provider body")
				}
			})
			err := handler(ctx, *j)
			var interval float64
			var message, code string
			var empty, started, success, due bool
			if queryErr := s.DB.QueryRow(t.Context(), `SELECT extract(epoch FROM "interval"),last_error,last_error_code,empty,last_started_at IS NOT NULL,last_success_at IS NOT NULL,next_due_at>clock_timestamp() FROM sync_state WHERE id=$1`, id).Scan(&interval, &message, &code, &empty, &started, &success, &due); queryErr != nil {
				t.Fatal(queryErr)
			}
			if !started || !due {
				t.Fatal("start/due metadata missing")
			}
			if scenario == "empty" || scenario == "account-wide-empty" {
				wantInterval, wantEmpty := float64(21600), true
				if scenario == "account-wide-empty" {
					wantInterval, wantEmpty = 300, false
				}
				if err != nil || empty != wantEmpty || !success || interval != wantInterval || message != "" || code != "" {
					t.Fatalf("empty result: err=%v interval=%v empty=%v success=%v message=%q", err, interval, empty, success, message)
				}
				if err := s.Queue.Complete(t.Context(), *j); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || message == "" || code == "" || success || interval != 300 || strings.Contains(message, "secret") || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unsafe/missing failure: err=%v message=%q code=%q", err, message, code)
				}
				if scenario == "cancelled" && code != "NX-SYNC-CANCELLED" {
					t.Fatalf("cancelled error not recorded: %s", code)
				}
				if err := s.Queue.Fail(t.Context(), *j, err.Error()); err != nil {
					t.Fatal(err)
				}
				var status string
				var delayed bool
				if err := s.DB.QueryRow(t.Context(), "SELECT status,run_after>clock_timestamp() FROM jobs WHERE id=$1", j.ID).Scan(&status, &delayed); err != nil {
					t.Fatal(err)
				}
				if status != "queued" || !delayed {
					t.Fatal("failed job was not scheduled to retry")
				}
				if _, err := s.DB.Exec(t.Context(), "UPDATE jobs SET run_after=now()-interval '1 second' WHERE id=$1", j.ID); err != nil {
					t.Fatal(err)
				}
				retry, err := s.Queue.Claim(t.Context(), "retry", time.Minute, syncer.JobKind)
				if err != nil || retry == nil || retry.ID != j.ID || retry.Attempts != 2 {
					t.Fatalf("retry: %+v %v", retry, err)
				}
				if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, nil })(t.Context(), *retry); err != nil {
					t.Fatal(err)
				}
				if err := s.DB.QueryRow(t.Context(), "SELECT last_error,last_error_code,last_success_at IS NOT NULL FROM sync_state WHERE id=$1", id).Scan(&message, &code, &success); err != nil {
					t.Fatal(err)
				}
				if message != "" || code != "" || !success {
					t.Fatal("successful retry did not clear failure")
				}
			}
		})
	}
}

func TestStaleAttemptCannotReplaceNewResult(t *testing.T) {
	s, _, account := setup(t)
	id := addState(t, s, account)
	old := queued(t, s)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) {
			close(started)
			<-release
			return false, errors.New("stale failure")
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
	if _, err := s.DB.Exec(t.Context(), "UPDATE jobs SET locked_until=now()-interval '1 second' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.Queue.Claim(t.Context(), "new-owner", time.Minute, syncer.JobKind)
	if err != nil || current == nil {
		t.Fatalf("replacement: %+v %v", current, err)
	}
	if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, nil })(t.Context(), *current); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	var clean bool
	if err := s.DB.QueryRow(t.Context(), "SELECT last_error='' AND last_attempt=2 AND last_success_at IS NOT NULL FROM sync_state WHERE id=$1", id).Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Fatal("stale attempt replaced newer metadata")
	}
}

func TestRestartKeepsScheduledWork(t *testing.T) {
	s, _, account := setup(t)
	addState(t, s, account)
	if _, err := s.EnqueueDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	old, err := s.Queue.Claim(t.Context(), "dead", 50*time.Millisecond, syncer.JobKind)
	if err != nil || old == nil {
		t.Fatalf("first claim: %+v %v", old, err)
	}
	restarted := syncer.New(s.DB, s.Config)
	if n, err := restarted.EnqueueDue(t.Context()); err != nil || n != 0 {
		t.Fatalf("restart duplicated job: %d %v", n, err)
	}
	var recovered *jobs.Job
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		recovered, err = restarted.Queue.Claim(t.Context(), "replacement", time.Minute, syncer.JobKind)
		if err != nil {
			t.Fatal(err)
		}
		if recovered != nil {
			break
		}
	}
	if recovered == nil || recovered.ID != old.ID || recovered.Attempts != 2 {
		t.Fatalf("lost scheduled work: %+v", recovered)
	}
	if err := restarted.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, nil })(t.Context(), *recovered); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Queue.Complete(t.Context(), *recovered); err != nil {
		t.Fatal(err)
	}
	if n, err := restarted.EnqueueDue(t.Context()); err != nil || n != 0 {
		t.Fatalf("rescheduled fresh scope: %d %v", n, err)
	}
}

func TestCleanupOnlyOldFinishedJobs(t *testing.T) {
	s, _, _ := setup(t)
	for _, status := range []string{"queued", "running", "succeeded", "failed"} {
		for _, age := range []string{"1 day", "8 days"} {
			_, err := s.DB.Exec(t.Context(), `INSERT INTO jobs(kind,status,attempts,locked_by,locked_until,created_at,updated_at)
                VALUES('test',$1,1,CASE WHEN $1='running' THEN 'owner' ELSE NULL END,
                CASE WHEN $1='running' THEN now()+interval '1 hour' ELSE NULL END,now()-interval '10 days',now()-$2::interval)`, status, age)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, want := range []int64{1, 1, 0} {
		n, err := s.Queue.Cleanup(t.Context(), 7*24*time.Hour, 1)
		if err != nil || n != want {
			t.Fatalf("cleanup: %d %v want %d", n, err, want)
		}
	}
	var total, active int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER (WHERE status IN ('queued','running')) FROM jobs`).Scan(&total, &active); err != nil {
		t.Fatal(err)
	}
	if total != 6 || active != 4 {
		t.Fatalf("deleted wrong jobs: total=%d active=%d", total, active)
	}
}
