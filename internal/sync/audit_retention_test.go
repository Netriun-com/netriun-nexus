// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/jobs"
	syncer "github.com/netriun/nexus/internal/sync"
)

func retentionSettings(t *testing.T, s *syncer.Store, workspace int64, days int) {
	t.Helper()
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO settings(workspace_id,audit_retention_days) VALUES($1,$2)`, workspace, days); err != nil {
		t.Fatal(err)
	}
}

func auditRows(t *testing.T, s *syncer.Store, workspace int64, age string, count int) {
	t.Helper()
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO audit_log(workspace_id,action,created_at) SELECT $1,'retention-test',now()-$2::interval FROM generate_series(1,$3::int)`, workspace, age, count); err != nil {
		t.Fatal(err)
	}
}

func auditCount(t *testing.T, s *syncer.Store, workspace int64) int {
	t.Helper()
	var count int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE workspace_id=$1`, workspace).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAuditRetentionJobWithoutSync(t *testing.T) {
	s, workspace, _ := setup(t)
	retentionSettings(t, s, workspace, 7)
	// More than two batches must be removed. Recent rows and other tenants stay.
	auditRows(t, s, workspace, "8 days", 1201)
	auditRows(t, s, workspace, "6 days", 2)
	var other int64
	if err := s.DB.QueryRow(t.Context(), `INSERT INTO workspaces(name,slug) VALUES('Other tenant','other') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	retentionSettings(t, s, other, 30)
	auditRows(t, s, other, "8 days", 3)
	auditRows(t, s, other, "31 days", 1)
	if n, err := s.EnqueueAuditRetention(t.Context()); err != nil || n != 2 {
		t.Fatalf("schedule: %d %v", n, err)
	}
	j, err := s.Queue.Claim(t.Context(), "retention", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if err := s.AuditRetentionHandler()(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if auditCount(t, s, workspace) != 2 || auditCount(t, s, other) != 4 {
		t.Fatal("retention crossed workspace or policy boundary")
	}
	if err := s.Queue.Complete(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	j, err = s.Queue.Claim(t.Context(), "retention", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || j == nil {
		t.Fatalf("second claim: %+v %v", j, err)
	}
	if err := s.AuditRetentionHandler()(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if auditCount(t, s, other) != 3 {
		t.Fatal("second workspace policy not applied")
	}
	if err := s.Queue.Complete(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	// Cadence is persisted independently of queue history and process memory.
	if _, err := s.DB.Exec(t.Context(), `DELETE FROM jobs WHERE kind=$1`, syncer.AuditRetentionJobKind); err != nil {
		t.Fatal(err)
	}
	restarted := syncer.New(s.DB, s.Config)
	if n, err := restarted.EnqueueAuditRetention(t.Context()); err != nil || n != 0 {
		t.Fatalf("restart ignored cadence: %d %v", n, err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE settings SET audit_retention_next_due_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := restarted.EnqueueAuditRetention(t.Context()); err != nil || n != 2 {
		t.Fatalf("next interval: %d %v", n, err)
	}
}

func TestConcurrentAuditRetentionSchedulers(t *testing.T) {
	s, workspace, _ := setup(t)
	retentionSettings(t, s, workspace, 7)
	s.Config.AuditRetentionInterval = 2 * time.Hour
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			other := syncer.New(s.DB, s.Config)
			if _, err := other.EnqueueAuditRetention(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	var count int
	var key, concurrency string
	var interval float64
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*),min(dedupe_key),min(concurrency_key) FROM jobs WHERE kind=$1`, syncer.AuditRetentionJobKind).Scan(&count, &key, &concurrency); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != fmt.Sprintf("audit.retention:%d", workspace) || concurrency != key {
		t.Fatalf("jobs=%d key=%s concurrency=%s", count, key, concurrency)
	}
	if err := s.DB.QueryRow(t.Context(), `SELECT extract(epoch FROM audit_retention_next_due_at-now()) FROM settings WHERE workspace_id=$1`, workspace).Scan(&interval); err != nil {
		t.Fatal(err)
	}
	if interval < 7190 || interval > 7210 {
		t.Fatalf("configured interval ignored: %v", interval)
	}
	// Even an overdue workspace with a running job must not get another job.
	j, err := s.Queue.Claim(t.Context(), "first", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE settings SET audit_retention_next_due_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EnqueueAuditRetention(t.Context()); err != nil || n != 0 {
		t.Fatalf("duplicated active job: %d %v", n, err)
	}
	if next, err := s.Queue.Claim(t.Context(), "second", time.Minute, syncer.AuditRetentionJobKind); err != nil || next != nil {
		t.Fatalf("concurrent job: %+v %v", next, err)
	}
}

func TestComputeSyncDoesNotRunAuditRetention(t *testing.T) {
	s, workspace, account := setup(t)
	retentionSettings(t, s, workspace, 7)
	auditRows(t, s, workspace, "8 days", 3)
	addState(t, s, account)
	j := queued(t, s)
	if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, nil })(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if auditCount(t, s, workspace) != 3 {
		t.Fatal("compute sync deleted audit rows")
	}
	if err := s.Queue.Complete(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	// Manual refreshes use this same scheduled-sync handler and result recorder.
	if _, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	j, err := s.Queue.Claim(t.Context(), "refresh", time.Minute, syncer.JobKind)
	if err != nil || j == nil {
		t.Fatalf("refresh claim: %+v %v", j, err)
	}
	if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) { return false, nil })(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	if auditCount(t, s, workspace) != 3 {
		t.Fatal("manual refresh deleted audit rows")
	}
}

func TestAuditRetentionRejectsLostLease(t *testing.T) {
	s, workspace, _ := setup(t)
	retentionSettings(t, s, workspace, 7)
	auditRows(t, s, workspace, "8 days", 1)
	if _, err := s.EnqueueAuditRetention(t.Context()); err != nil {
		t.Fatal(err)
	}
	j, err := s.Queue.Claim(t.Context(), "stale", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || j == nil {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE jobs SET locked_until=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuditRetentionHandler()(t.Context(), *j); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("lost lease: %v", err)
	}
	if auditCount(t, s, workspace) != 1 {
		t.Fatal("stale worker deleted audit rows")
	}
	retry, err := s.Queue.Claim(t.Context(), "replacement", time.Minute, syncer.AuditRetentionJobKind)
	if err != nil || retry == nil {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if err := s.AuditRetentionHandler()(t.Context(), *retry); err != nil {
		t.Fatal(err)
	}
	if auditCount(t, s, workspace) != 0 {
		t.Fatal("replacement did not clean audit rows")
	}
}
