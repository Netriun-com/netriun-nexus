// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/jobs"
)

func (s *Store) recordResult(ctx context.Context, p Payload, j jobs.Job, message, code string, empty bool, interval time.Duration) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Match the account -> lease -> state lock order used by catalog writers.
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, p.AccountID, p.WorkspaceID).Scan(&id); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE sync_state SET
        last_success_at=CASE WHEN $4='' THEN clock_timestamp() ELSE last_success_at END,
        last_error=$4,last_error_code=$5,empty=CASE WHEN $4='' THEN $6 WHEN service='compute' THEN empty ELSE false END,
        "interval"=$7::bigint*interval '1 microsecond',next_due_at=clock_timestamp()+$7::bigint*interval '1 microsecond'
        WHERE id=$1 AND enabled AND last_job_id=$2 AND last_attempt=$3`, p.StateID, j.ID, j.Attempts, message, code, empty, interval.Microseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if p.Service == Compute {
		if err := s.computeSummary(ctx, tx, p.AccountID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE settings SET last_collector_run_at=clock_timestamp() WHERE workspace_id=$1`, p.WorkspaceID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO resource_events(workspace_id,account_id,type,service,region,success,error_code)
 VALUES($1,$2,'sync.finished',$3,$4,$5,$6)`, p.WorkspaceID, p.AccountID, p.Service, p.Region, code == "", code); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) computeSummary(ctx context.Context, tx pgx.Tx, account int64) error {
	_, err := tx.Exec(ctx, `UPDATE cloud_accounts SET
      last_sync_at=(SELECT CASE WHEN count(*)=count(last_success_at) THEN min(last_success_at) ELSE NULL END
          FROM sync_state WHERE account_id=$1 AND service='compute' AND enabled),
      sync_error=COALESCE((SELECT last_error FROM sync_state WHERE account_id=$1 AND service='compute' AND enabled AND last_error<>'' ORDER BY last_started_at DESC,id LIMIT 1),''),
      sync_error_code=COALESCE((SELECT last_error_code FROM sync_state WHERE account_id=$1 AND service='compute' AND enabled AND last_error<>'' ORDER BY last_started_at DESC,id LIMIT 1),'')
      WHERE id=$1`, account)
	return err
}
