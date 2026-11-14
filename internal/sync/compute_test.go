// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/dara"
	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/jobs"
	syncer "github.com/netriun/nexus/internal/sync"
	"github.com/netriun/nexus/internal/worker"
)

func TestManualJoinsScheduleAndScopesStatus(t *testing.T) {
	s, workspace, account := setup(t)
	changeAccount(t, s, account, "aws", []string{"us-east-1", "us-west-2"})
	if _, err := s.EnqueueDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	var count, priority int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*),max(priority) FROM jobs`).Scan(&count, &priority); err != nil {
		t.Fatal(err)
	}
	if count != 2 || priority != 100 {
		t.Fatalf("duplicate or unpromoted schedule: %d %d", count, priority)
	}
	j, err := s.Queue.Claim(t.Context(), "manual", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %v", err)
	}
	same, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "us-east-1")
	if err != nil || same.ID != receipt.ID {
		t.Fatalf("running job not joined: %+v %v", same, err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace+1, receipt.ID, []int64{account}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("workspace isolation: %v", err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace, receipt.ID, nil); !errors.Is(err, syncer.ErrAccountAccess) {
		t.Fatalf("account isolation: %v", err)
	}
	if _, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "unknown"); !errors.Is(err, syncer.ErrNoScopes) {
		t.Fatalf("unknown region: %v", err)
	}
	// Even a receipt referencing another workspace's executable job is denied.
	if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET payload=jsonb_set(payload,'{workspace_id}',to_jsonb($2::bigint)) WHERE id=$1`, j.ID, workspace+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace, receipt.ID, []int64{account}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("job payload workspace not enforced: %v", err)
	}
}

func TestGlobalManualScopeAndRetryPriority(t *testing.T) {
	s, workspace, account := setup(t)
	changeAccount(t, s, account, "azure", []string{"eastus"})
	r, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "eastus")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Queue.Claim(t.Context(), "worker", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	if j.DedupeKey != fmt.Sprintf("sync:%d:compute:*", account) || j.ProviderKey != "provider:azure" {
		t.Fatalf("global scope: %+v", j)
	}
	if err := s.Queue.Fail(t.Context(), *j, "safe failure"); err != nil {
		t.Fatal(err)
	}
	again, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "")
	if err != nil || again.ID != r.ID {
		t.Fatalf("retry joined: %+v %v", again, err)
	}
	var delayed bool
	if err := s.DB.QueryRow(t.Context(), `SELECT run_after>clock_timestamp() FROM jobs WHERE id=$1`, j.ID).Scan(&delayed); err != nil || !delayed {
		t.Fatalf("manual bypassed backoff: %v", err)
	}
}

func TestSlowAccountDoesNotBlockAnother(t *testing.T) {
	s, workspace, slow := setup(t)
	changeAccount(t, s, slow, "aws", []string{"one", "two", "three"})
	var fast int64
	if err := s.DB.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'Fast','aws','test',ARRAY['one']) RETURNING id`, workspace).Scan(&fast); err != nil {
		t.Fatal(err)
	}
	changeAccount(t, s, fast, "aws", []string{"one"})
	if _, err := s.RefreshCompute(t.Context(), workspace, []int64{slow}, ""); err != nil {
		t.Fatal(err)
	}
	q, err := jobs.New(s.DB, map[string]int{"account:*": 1, "provider:*": 2})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}, 3), make(chan struct{})
	h := s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
		if p.AccountID == slow {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		return false, nil
	})
	cfg := worker.DefaultConfig()
	cfg.Concurrency = 2
	cfg.PollInterval = 10 * time.Millisecond
	w, err := worker.New(q, worker.Registry{syncer.JobKind: h}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { close(release); cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow provider never started")
	}
	r, err := s.RefreshCompute(t.Context(), workspace, []int64{fast}, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := s.RefreshStatus(t.Context(), workspace, r.ID, []int64{fast})
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow account blocked fast account")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var running int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE status='running' AND concurrency_key=$1`, fmt.Sprintf("account:%d", slow)).Scan(&running); err != nil || running != 1 {
		t.Fatalf("account limit not enforced: %d %v", running, err)
	}
}

func TestExpiredAttemptCannotOverwriteInventory(t *testing.T) {
	s, _, account := setup(t)
	addState(t, s, account)
	old := queued(t, s)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
			close(started)
			<-release
			return false, s.ReconcileInventory(ctx, account, []cloud.Instance{{ID: "stale", Region: p.Region}}, false, p.Region)
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
	current, err := s.Queue.Claim(t.Context(), "replacement", time.Minute, syncer.JobKind)
	if err != nil || current == nil {
		t.Fatal(err)
	}
	if err := s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
		return false, s.ReconcileInventory(ctx, account, []cloud.Instance{{ID: "fresh", Region: p.Region}}, false, p.Region)
	})(t.Context(), *current); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale write accepted")
	}
	var id string
	if err := s.DB.QueryRow(t.Context(), `SELECT instance_id FROM instances WHERE account_id=$1`, account).Scan(&id); err != nil || id != "fresh" {
		t.Fatalf("new inventory overwritten: %s %v", id, err)
	}
}

type authError struct{}

func (authError) Error() string     { return "secret provider response" }
func (authError) ErrorCode() string { return "UnauthorizedOperation" }

func TestComputeFreshnessAndSafeErrors(t *testing.T) {
	s, workspace, account := setup(t)
	changeAccount(t, s, account, "aws", []string{"one", "two"})
	run := func(region string, failure error) {
		t.Helper()
		if _, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, region); err != nil {
			t.Fatal(err)
		}
		j, err := s.Queue.Claim(t.Context(), "worker", time.Minute, syncer.JobKind)
		if err != nil || j == nil {
			t.Fatalf("claim: %v", err)
		}
		err = s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, failure })(t.Context(), *j)
		if (err == nil) != (failure == nil) {
			t.Fatalf("result: %v", err)
		}
		if err := s.Queue.Complete(t.Context(), *j); err != nil {
			t.Fatal(err)
		} // isolate each metadata case
	}
	run("one", nil)
	var last *time.Time
	if err := s.DB.QueryRow(t.Context(), `SELECT last_sync_at FROM cloud_accounts WHERE id=$1`, account).Scan(&last); err != nil || last != nil {
		t.Fatalf("uncollected region reported fresh: %v %v", last, err)
	}
	run("two", nil)
	var oldest time.Time
	if err := s.DB.QueryRow(t.Context(), `SELECT min(last_success_at) FROM sync_state WHERE account_id=$1`, account).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(t.Context(), `SELECT last_sync_at FROM cloud_accounts WHERE id=$1`, account).Scan(&last); err != nil || last == nil || !last.Equal(oldest) {
		t.Fatalf("wrong freshness: %v %v", last, err)
	}
	for _, tc := range []struct {
		err  error
		code string
	}{{authError{}, "NX-SYNC-AUTH"}, {&dara.SDKError{Code: dara.String("InvalidAccessKeyId.NotFound"), Message: dara.String("secret response")}, "NX-SYNC-AUTH"}, {errors.New("Azure request failed with status 403"), "NX-SYNC-AUTH"}, {errors.New("GCP request failed with status 401"), "NX-SYNC-AUTH"}, {context.DeadlineExceeded, "NX-SYNC-TIMEOUT"}, {context.Canceled, "NX-SYNC-CANCELLED"}, {errors.New("secret body"), "NX-SYNC-PROVIDER"}} {
		run("two", tc.err)
		var code, message string
		if err := s.DB.QueryRow(t.Context(), `SELECT sync_error_code,sync_error FROM cloud_accounts WHERE id=$1`, account).Scan(&code, &message); err != nil || code != tc.code {
			t.Fatalf("wrong account error: %s %s %v", code, message, err)
		}
		expectedCode, expectedMessage := syncer.ComputeError(tc.err)
		if code != expectedCode || message != expectedMessage {
			t.Fatal("unsafe error persisted")
		}
	}
	run("one", nil)
	var code string
	if err := s.DB.QueryRow(t.Context(), `SELECT sync_error_code FROM cloud_accounts WHERE id=$1`, account).Scan(&code); err != nil || code == "" {
		t.Fatal("success hid another region's failure")
	}
	run("two", nil)
	if err := s.DB.QueryRow(t.Context(), `SELECT sync_error_code FROM cloud_accounts WHERE id=$1`, account).Scan(&code); err != nil || code != "" {
		t.Fatal("recovery did not clear error")
	}
}

func TestDiscoveryCadenceAndReplicaLock(t *testing.T) {
	s, _, account := setup(t)
	changeAccount(t, s, account, "aws", []string{})
	var calls atomic.Int32
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	discover := func(ctx context.Context, _ syncer.Account) map[string][]string {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return map[string][]string{syncer.Compute: {"us-east-1"}}
	}
	go func() { done <- s.Discover(t.Context(), discover) }()
	<-started
	if err := syncer.New(s.DB, s.Config).Discover(t.Context(), func(context.Context, syncer.Account) map[string][]string {
		t.Error("second replica discovered concurrently")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Discover(t.Context(), func(context.Context, syncer.Account) map[string][]string { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("discovery repeated before hourly cadence")
	}
	var delay float64
	if err := s.DB.QueryRow(t.Context(), `SELECT extract(epoch FROM next_due_at-clock_timestamp()) FROM sync_discovery WHERE account_id=$1`, account).Scan(&delay); err != nil || delay < 3590 {
		t.Fatalf("discovery cadence: %v %v", delay, err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE sync_discovery SET next_due_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Discover(t.Context(), func(context.Context, syncer.Account) map[string][]string { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(scopes(t, s, account)) != 1 {
		t.Fatal("failed discovery discarded known scopes")
	}
}

func TestFailedReceiptAndRetention(t *testing.T) {
	s, workspace, account := setup(t)
	addState(t, s, account)
	r, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE inventory_refreshes SET created_at=now()-interval '30 days' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupRefreshes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace, r.ID, []int64{account}); err != nil {
		t.Fatal("active receipt expired", err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET max_attempts=1`); err != nil {
		t.Fatal(err)
	}
	j, err := s.Queue.Claim(t.Context(), "failure", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	err = s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, authError{} })(t.Context(), *j)
	if err == nil {
		t.Fatal("expected failure")
	}
	if err := s.Queue.Fail(t.Context(), *j, err.Error()); err != nil {
		t.Fatal(err)
	}
	status, err := s.RefreshStatus(t.Context(), workspace, r.ID, []int64{account})
	if err != nil || status.Status != "partial" || len(status.FailedAccounts) != 1 || status.FailedAccounts[0].Code != "NX-SYNC-AUTH" {
		t.Fatalf("failure response: %+v %v", status, err)
	}
	if status.StartedAt == nil || status.CompletedAt == nil || status.DurationMS < 0 {
		t.Fatal("missing job timestamps")
	}
	if err := s.CleanupRefreshes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace, r.ID, []int64{account}); err != nil {
		t.Fatal("recent completion expired based on receipt age", err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET updated_at=now()-interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queue.Cleanup(t.Context(), s.Config.Retention, 500); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupRefreshes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshStatus(t.Context(), workspace, r.ID, []int64{account}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired receipt still readable: %v", err)
	}
}
