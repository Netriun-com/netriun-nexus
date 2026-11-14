// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"
)

// Result checks the snapshot produced by an existing collector. Partial results
// never mark a scope successful or empty. No provider response is persisted as an error.
func (s *Store) Result(ctx context.Context, p Payload, snapshotRegion string) (bool, error) {
	if p.Service == Compute {
		var empty bool
		err := s.DB.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM instances i JOIN cloud_accounts a ON a.id=i.account_id
            WHERE a.workspace_id=$1 AND a.id=$2 AND ($3='*' OR i.region=$3))`, p.WorkspaceID, p.AccountID, p.Region).Scan(&empty)
		return empty && p.Region != AccountWide, err
	}
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT payload FROM service_snapshots WHERE workspace_id=$1 AND account_id=$2 AND service_key=$3 AND region=$4`, p.WorkspaceID, p.AccountID, p.Service, snapshotRegion).Scan(&raw)
	if err != nil {
		return false, err
	}
	var snapshot struct {
		Data          []json.RawMessage `json:"data"`
		FailedRegions []string          `json:"failed_regions"`
		FailedBuckets []string          `json:"failed_buckets"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return false, err
	}
	if len(snapshot.FailedRegions) > 0 || len(snapshot.FailedBuckets) > 0 {
		return false, errors.New("Collection was incomplete")
	}
	return len(snapshot.Data) == 0 && p.Region != AccountWide, nil
}
