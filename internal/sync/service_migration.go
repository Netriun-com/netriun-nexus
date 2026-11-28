// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// migrateServiceScopes preserves scheduling history when replacing account-wide
// scopes. Directory partitions must each obtain their own successful collection.
func (s *Store) migrateServiceScopes(ctx context.Context, tx pgx.Tx, a Account, service string, regions []string) error {
	if service != SecurityGroups && service != Users && service != Desktops {
		return nil
	}
	for _, region := range regions {
		_, err := tx.Exec(ctx, `INSERT INTO sync_state(account_id,service,region,"interval",last_started_at,last_success_at,last_error,last_error_code,enabled,next_due_at)
 SELECT account_id,service,$3,"interval",last_started_at,
 CASE WHEN $2='eds.users' THEN (SELECT min(r.seen_at) FROM resources r WHERE r.workspace_id=$4 AND r.account_id=$1 AND r.service=$2 AND r.kind='directory_user' AND r.region=$3) ELSE last_success_at END,
 last_error,last_error_code,enabled,next_due_at FROM sync_state WHERE account_id=$1 AND service=$2 AND region='*'
 ON CONFLICT(account_id,service,region) DO NOTHING`, a.ID, service, region, a.WorkspaceID)
		if err != nil {
			return err
		}
	}
	return nil
}
