// SPDX-License-Identifier: AGPL-3.0-only
package sync

import "context"

// Result observes only committed catalog rows. A failed collector never calls it.
func (s *Store) Result(ctx context.Context, p Payload, snapshotRegion string) (bool, error) {
	var empty bool
	err := s.DB.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service=$3 AND ($4='*' OR region=$4))`, p.WorkspaceID, p.AccountID, p.Service, p.Region).Scan(&empty)
	return empty && p.Region != AccountWide, err
}
