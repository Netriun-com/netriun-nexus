// SPDX-License-Identifier: AGPL-3.0-only

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each test applies the real migration in its own schema: the application
// integration test may migrate/truncate public concurrently under go test ./....
func testQueue(t *testing.T, limits map[string]int) *Queue {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL queue tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Database != "ccmp_test" {
		t.Fatal("test database must be named ccmp_test")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("jobs_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 24
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	migrations, err := filepath.Glob("../app/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range migrations {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	q, err := New(db, limits)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func enqueue(t *testing.T, q *Queue, p Params) *Job {
	t.Helper()
	j, err := q.Enqueue(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAccountAndProviderLimitsTogether(t *testing.T) {
	q := testQueue(t, map[string]int{"account:*": 1, "provider:*": 2})
	for _, account := range []string{"account:1", "account:2", "account:3"} {
		for range 3 {
			enqueue(t, q, Params{Kind: "compute", ConcurrencyKey: account, ProviderKey: "provider:aws"})
		}
	}
	enqueue(t, q, Params{Kind: "compute", ConcurrencyKey: "account:4", ProviderKey: "provider:azure"})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 4 {
				if _, err := q.Claim(t.Context(), fmt.Sprint("worker-", i), time.Minute); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	// Fill any capacity left by try-lock contention after the concurrent claims.
	for range 4 {
		if _, err := q.Claim(t.Context(), "final", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	var aws, azure, accounts int
	if err := q.db.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE provider_key='provider:aws'),count(*) FILTER(WHERE provider_key='provider:azure'),count(DISTINCT concurrency_key) FROM jobs WHERE status='running'`).Scan(&aws, &azure, &accounts); err != nil {
		t.Fatal(err)
	}
	if aws != 2 || azure != 1 || accounts != 3 {
		t.Fatalf("combined limits: aws=%d azure=%d accounts=%d", aws, azure, accounts)
	}
}

func claim(t *testing.T, q *Queue, worker string) *Job {
	t.Helper()
	j, err := q.Claim(t.Context(), worker, time.Minute)
	if err != nil || j == nil {
		t.Fatalf("claim: job=%+v err=%v", j, err)
	}
	return j
}

func noClaim(t *testing.T, q *Queue) {
	t.Helper()
	j, err := q.Claim(t.Context(), "idle", time.Minute)
	if err != nil || j != nil {
		t.Fatalf("expected no claim: job=%+v err=%v", j, err)
	}
}

func readJob(t *testing.T, q *Queue, id int64) *Job {
	t.Helper()
	j, err := scan(q.db.QueryRow(t.Context(), "SELECT "+columns+" FROM jobs WHERE id=$1", id))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func execSQL(t *testing.T, q *Queue, sql string, args ...any) {
	t.Helper()
	if _, err := q.db.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// Start separate Queue instances together, as independent workers would do.
func claimTogether(t *testing.T, q *Queue, n int) []*Job {
	t.Helper()
	start := make(chan struct{})
	results := make(chan *Job, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			worker, err := New(q.db, q.limits)
			if err != nil {
				t.Error(err)
				return
			}
			<-start
			j, err := worker.Claim(t.Context(), fmt.Sprintf("worker-%d", i), time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if j != nil {
				results <- j
			}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var claimed []*Job
	seen := map[int64]bool{}
	for j := range results {
		if seen[j.ID] {
			t.Errorf("job %d was claimed twice", j.ID)
		}
		seen[j.ID] = true
		claimed = append(claimed, j)
	}
	return claimed
}

func TestConcurrentClaims(t *testing.T) {
	q := testQueue(t, nil)
	for range 24 {
		enqueue(t, q, Params{Kind: "test", Payload: json.RawMessage(`{"account":42}`)})
	}
	claimed := claimTogether(t, q, 24)
	if len(claimed) != 24 {
		t.Fatalf("claimed %d of 24 jobs", len(claimed))
	}
	for _, j := range claimed {
		if j.Status != "running" || j.Attempts != 1 || j.LockedUntil == nil {
			t.Fatalf("invalid claim: %+v", j)
		}
		if string(j.Payload) != `{"account": 42}` {
			t.Fatalf("lost JSON payload: %s", j.Payload)
		}
	}
	noClaim(t, q)
}

func TestClaimKindsAndScopeLimits(t *testing.T) {
	q := testQueue(t, map[string]int{"account:*": 2, "provider:*": 3, "account:special": 1})
	for _, key := range []string{"account:123", "provider:aws", "account:special"} {
		for range 5 {
			enqueue(t, q, Params{Kind: "supported", ConcurrencyKey: key})
		}
	}
	unknown := enqueue(t, q, Params{Kind: "future", Priority: 100})
	counts := map[string]int{}
	for {
		j, err := q.Claim(t.Context(), "worker", time.Minute, "supported")
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			break
		}
		counts[j.ConcurrencyKey]++
	}
	if counts["account:123"] != 2 || counts["provider:aws"] != 3 || counts["account:special"] != 1 {
		t.Fatalf("namespace/exact limits: %v", counts)
	}
	if j := readJob(t, q, unknown.ID); j.Status != "queued" || j.Attempts != 0 {
		t.Fatalf("unknown kind consumed: %+v", j)
	}
}

func TestConcurrencyLimits(t *testing.T) {
	q := testQueue(t, map[string]int{"account-a": 3})
	for range 24 {
		enqueue(t, q, Params{Kind: "test", ConcurrencyKey: "account-a", Priority: 10})
	}
	for range 8 {
		enqueue(t, q, Params{Kind: "test", ConcurrencyKey: "account-b"})
	}
	seen := map[int64]bool{}
	for round := range 8 {
		claimed := claimTogether(t, q, 16)
		// Busy advisory locks may return nil transiently. Fill remaining slots
		// after contenders finish; a saturated high-priority key must not
		// prevent account-b from making progress.
		for {
			j, err := q.Claim(t.Context(), "fill", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if j == nil {
				break
			}
			claimed = append(claimed, j)
		}
		counts := map[string]int{}
		for _, j := range claimed {
			if seen[j.ID] {
				t.Fatalf("job %d repeated in round %d", j.ID, round)
			}
			seen[j.ID] = true
			counts[j.ConcurrencyKey]++
		}
		if counts["account-a"] != 3 || counts["account-b"] != 1 {
			t.Fatalf("wrong concurrency: %v", counts)
		}
		var running int
		if err := q.db.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE status='running'").Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running != 4 {
			t.Fatalf("running=%d, want 4", running)
		}
		for _, j := range claimed {
			if err := q.Complete(t.Context(), *j); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) != 32 {
		t.Fatalf("completed %d jobs", len(seen))
	}
}

func TestEnqueueDedupe(t *testing.T) {
	q := testQueue(t, nil)
	start := make(chan struct{})
	results := make(chan *Job, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			j, err := q.Enqueue(t.Context(), Params{Kind: "first", DedupeKey: "refresh", Payload: json.RawMessage(`{"first":true}`)})
			if err != nil {
				t.Error(err)
				return
			}
			results <- j
		})
	}
	close(start)
	wg.Wait()
	close(results)
	first := enqueue(t, q, Params{Kind: "ignored", DedupeKey: "refresh"})
	for j := range results {
		if j.ID != first.ID || j.Kind != "first" || string(j.Payload) != string(first.Payload) || !j.UpdatedAt.Equal(first.UpdatedAt) {
			t.Errorf("dedupe changed existing job: %+v", j)
		}
	}
	running := claim(t, q, "worker")
	if j := enqueue(t, q, Params{Kind: "ignored", DedupeKey: "refresh"}); j.ID != running.ID || j.Status != "running" {
		t.Fatalf("running dedupe: %+v", j)
	}
	if err := q.Complete(t.Context(), *running); err != nil {
		t.Fatal(err)
	}
	next := enqueue(t, q, Params{Kind: "next", DedupeKey: "refresh", MaxAttempts: 1})
	if next.ID == first.ID {
		t.Fatal("completed job prevented enqueue")
	}
	if err := q.Fail(t.Context(), *claim(t, q, "worker"), "terminal"); err != nil {
		t.Fatal(err)
	}
	if j := enqueue(t, q, Params{Kind: "after failure", DedupeKey: "refresh"}); j.ID == next.ID {
		t.Fatal("failed job prevented enqueue")
	}
	a := enqueue(t, q, Params{Kind: "no dedupe"})
	b := enqueue(t, q, Params{Kind: "no dedupe"})
	if a.ID == b.ID {
		t.Fatal("empty keys should not deduplicate")
	}
}

func TestLeaseRecoveryAndFencing(t *testing.T) {
	q := testQueue(t, nil)
	enqueue(t, q, Params{Kind: "test", ConcurrencyKey: "account", MaxAttempts: 2})
	old, err := q.Claim(t.Context(), "old-worker", 50*time.Millisecond)
	if err != nil || old == nil {
		t.Fatalf("claim: %v %v", old, err)
	}
	// Let a real lease expire without renewing. Poll with another worker,
	// avoiding assumptions about how quickly the database responds.
	deadline := time.Now().Add(5 * time.Second)
	var recovered *Job
	for time.Now().Before(deadline) {
		recovered, err = q.Claim(t.Context(), "new-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if recovered != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if recovered == nil || recovered.ID != old.ID || recovered.Attempts != 2 || recovered.LastError != "lease expired" {
		t.Fatalf("not recovered: %+v", recovered)
	}
	for name, action := range map[string]func() error{
		"renew":    func() error { return q.Renew(t.Context(), *old, time.Minute) },
		"complete": func() error { return q.Complete(t.Context(), *old) },
		"fail":     func() error { return q.Fail(t.Context(), *old, "stale error") },
	} {
		if err := action(); !errors.Is(err, ErrLeaseLost) {
			t.Errorf("stale %s: %v", name, err)
		}
	}
	// Attempts also fence a worker ID reused after restart.
	stale := *old
	stale.LockedBy = recovered.LockedBy
	if err := q.Complete(t.Context(), stale); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale attempt: %v", err)
	}
	execSQL(t, q, "UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE id=$1", recovered.ID)
	if err := q.Renew(t.Context(), *recovered, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("renew expired: %v", err)
	}
	if n, err := q.ReclaimExpired(t.Context()); err != nil || n != 1 {
		t.Fatalf("reclaim: %d %v", n, err)
	}
	j := readJob(t, q, recovered.ID)
	if j.Status != "failed" || j.Attempts != 2 || j.LockedUntil != nil || j.LockedBy != "" {
		t.Fatalf("exhausted lease: %+v", j)
	}
	noClaim(t, q)
}

func TestRenewAndComplete(t *testing.T) {
	q := testQueue(t, nil)
	enqueue(t, q, Params{Kind: "test", ConcurrencyKey: "account"})
	j := claim(t, q, "worker")
	if err := q.Renew(t.Context(), *j, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	renewed := readJob(t, q, j.ID)
	if !renewed.LockedUntil.After(*j.LockedUntil) || renewed.Attempts != 1 {
		t.Fatalf("renewed: %+v", renewed)
	}
	if err := q.Renew(t.Context(), *j, time.Second); err != nil {
		t.Fatal(err)
	}
	if !readJob(t, q, j.ID).LockedUntil.Equal(*renewed.LockedUntil) {
		t.Fatal("renewal shortened lease")
	}
	if n, err := q.ReclaimExpired(t.Context()); err != nil || n != 0 {
		t.Fatalf("reclaimed live job: %d %v", n, err)
	}
	noClaim(t, q)
	wrong := *j
	wrong.LockedBy = "other"
	if err := q.Complete(t.Context(), wrong); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong owner: %v", err)
	}
	if err := q.Complete(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	done := readJob(t, q, j.ID)
	if done.Status != "succeeded" || done.LockedBy != "" || done.LockedUntil != nil {
		t.Fatalf("complete: %+v", done)
	}
	if err := q.Fail(t.Context(), *j, "too late"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completed lease: %v", err)
	}
}

func TestFailureBackoff(t *testing.T) {
	q := testQueue(t, nil)
	original := enqueue(t, q, Params{Kind: "test", MaxAttempts: 12})
	for attempt := 1; attempt <= 12; attempt++ {
		j := claim(t, q, "worker")
		if j.ID != original.ID || j.Attempts != attempt {
			t.Fatalf("attempt %d: %+v", attempt, j)
		}
		if err := q.Fail(t.Context(), *j, fmt.Sprintf("error %d", attempt)); err != nil {
			t.Fatal(err)
		}
		failed := readJob(t, q, j.ID)
		if failed.LockedUntil != nil || failed.LockedBy != "" || failed.LastError != fmt.Sprintf("error %d", attempt) {
			t.Fatalf("failure state: %+v", failed)
		}
		if attempt == 12 {
			if failed.Status != "failed" {
				t.Fatalf("not terminal: %+v", failed)
			}
		} else {
			want := min(time.Second<<(attempt-1), 15*time.Minute)
			delay := failed.RunAfter.Sub(failed.UpdatedAt)
			if failed.Status != "queued" || delay < want-100*time.Millisecond || delay > want+100*time.Millisecond {
				t.Fatalf("attempt %d delay=%s want=%s status=%s", attempt, delay, want, failed.Status)
			}
		}
		noClaim(t, q)
		// Advance the persisted schedule instead of sleeping through backoff.
		execSQL(t, q, "UPDATE jobs SET run_after=clock_timestamp()-interval '1 second' WHERE id=$1", j.ID)
	}
	noClaim(t, q)
}

func TestDuePriorityAndSkipLocked(t *testing.T) {
	q := testQueue(t, nil)
	low := enqueue(t, q, Params{Kind: "low"})
	high := enqueue(t, q, Params{Kind: "high", Priority: 10})
	enqueue(t, q, Params{Kind: "future", Priority: 100, RunAfter: time.Now().Add(time.Hour)})
	if j := claim(t, q, "worker"); j.ID != high.ID {
		t.Fatalf("not highest due priority: %+v", j)
	}
	tx, err := q.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), "SELECT id FROM jobs WHERE id=$1 FOR UPDATE", low.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if j, err := q.Claim(ctx, "skip", time.Minute); err != nil || j != nil {
		t.Fatalf("did not skip row lock: %+v %v", j, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if j := claim(t, q, "worker"); j.ID != low.ID {
		t.Fatalf("not low job: %+v", j)
	}
	noClaim(t, q)
}
