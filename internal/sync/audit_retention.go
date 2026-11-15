// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/worker"
)

const AuditRetentionJobKind = "audit.retention"

const auditRetentionBatch = 500

type auditRetentionPayload struct {
	WorkspaceID int64 `json:"workspace_id"`
}

// EnqueueAuditRetention atomically advances persisted workspace due times and
// enqueues jobs. Row locks and queue deduplication serialize scheduler replicas.
// New workspaces are due immediately, including those without cloud accounts.
func (s *Store) EnqueueAuditRetention(ctx context.Context) (int, error) {
	if s.Config.AuditRetentionInterval <= 0 {
		return 0, errors.New("audit retention interval must be positive")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT workspace_id FROM settings s
        WHERE audit_retention_next_due_at<=clock_timestamp()
        AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.status IN ('queued','running')
            AND j.dedupe_key='audit.retention:'||s.workspace_id)
        ORDER BY audit_retention_next_due_at,workspace_id LIMIT 100 FOR UPDATE OF s SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	var workspaces []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		workspaces = append(workspaces, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range workspaces {
		payload, _ := json.Marshal(auditRetentionPayload{WorkspaceID: id})
		key := fmt.Sprintf("audit.retention:%d", id)
		if _, err := s.Queue.EnqueueTx(ctx, tx, jobs.Params{Kind: AuditRetentionJobKind,
			Payload: payload, DedupeKey: key, ConcurrencyKey: key}); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE settings SET audit_retention_next_due_at=
            clock_timestamp()+$2::bigint*interval '1 microsecond' WHERE workspace_id=$1`, id, s.Config.AuditRetentionInterval.Microseconds()); err != nil {
			return 0, err
		}
	}
	return len(workspaces), tx.Commit(ctx)
}

// AuditRetentionHandler deletes expired audit rows in short, lease-fenced
// transactions. Each batch reads the current workspace policy. Locked rows are
// left for the next run; cancelled or failed jobs can safely retry partial work.
func (s *Store) AuditRetentionHandler() worker.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var p auditRetentionPayload
		if json.Unmarshal(j.Payload, &p) != nil || p.WorkspaceID <= 0 {
			return errors.New("invalid audit retention payload")
		}
		for {
			n, err := s.deleteAuditBatch(ctx, j, p.WorkspaceID)
			if err != nil || n < auditRetentionBatch {
				return err
			}
		}
	}
}

func (s *Store) deleteAuditBatch(ctx context.Context, j jobs.Job, workspace int64) (int64, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `WITH expired AS (
        SELECT id FROM audit_log WHERE workspace_id=$1 AND created_at < now()-make_interval(
            days=>(SELECT audit_retention_days FROM settings WHERE workspace_id=$1))
        ORDER BY created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED
    ) DELETE FROM audit_log WHERE id IN (SELECT id FROM expired)`, workspace, auditRetentionBatch)
	if err != nil {
		return 0, err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
