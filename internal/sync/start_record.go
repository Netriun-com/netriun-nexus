// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/jobs"
)

// recordStart commits the state transition and event together before collecting.
// The account -> lease -> state lock order matches the catalog writers.
func (s *Store) recordStart(ctx context.Context, p Payload, j jobs.Job, ac *Account) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := tx.QueryRow(ctx, `SELECT provider,regions FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, p.AccountID, p.WorkspaceID).Scan(&ac.Provider, &ac.Regions); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE sync_state SET last_started_at=clock_timestamp(),last_job_id=$1,last_attempt=$2
 WHERE id=$3 AND account_id=$4 AND service=$5 AND region=$6 AND enabled
 AND NOT(region='*' AND service IN ('eds.users','ecs.security_groups'))`, j.ID, j.Attempts, p.StateID, p.AccountID, p.Service, p.Region)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `INSERT INTO resource_events(workspace_id,account_id,type,service,region)
 VALUES($1,$2,'sync.started',$3,$4)`, p.WorkspaceID, p.AccountID, p.Service, p.Region); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, j); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
