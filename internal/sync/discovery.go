// SPDX-License-Identifier: AGPL-3.0-only

package sync

import "context"

// Discover runs at most once per account per configured interval across all
// replicas. The advisory transaction lock covers provider calls as well as row
// reconciliation; a process exit releases it. No inventory jobs wait on it.
func (s *Store) Discover(ctx context.Context, discover Discovery) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('nexus.sync.discovery:'||current_schema(),0))`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.workspace_id,a.provider,a.regions FROM sync_discovery d JOIN cloud_accounts a ON a.id=d.account_id
        WHERE d.next_due_at<=clock_timestamp() AND cardinality(a.regions)=0 AND a.provider IN ('aws','alibaba') ORDER BY d.next_due_at,a.id LIMIT 100`)
	if err != nil {
		return err
	}
	var accounts []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Provider, &a.Regions); err != nil {
			rows.Close()
			return err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range accounts {
		found := discover(ctx, a)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Commit each account separately. The outer transaction owns only the
		// advisory lock, so a slow provider never holds earlier account rows.
		save, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		err = s.reconcileAccount(ctx, save, a.ID, found, &a)
		if err == nil {
			_, err = save.Exec(ctx, `UPDATE sync_discovery SET next_due_at=clock_timestamp()+$2::bigint*interval '1 microsecond' WHERE account_id=$1`, a.ID, s.Config.Discovery.Microseconds())
		}
		if err == nil {
			err = save.Commit(ctx)
		}
		_ = save.Rollback(context.Background())
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
