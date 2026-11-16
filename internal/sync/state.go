// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"time"
)

// State is the read-only freshness and queue metadata for one enabled scope.
type State struct {
	AccountID     int64      `json:"account_id"`
	Service       string     `json:"service"`
	Region        string     `json:"region"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastStartedAt *time.Time `json:"last_started_at"`
	NextDueAt     time.Time  `json:"next_due_at"`
	LastError     string     `json:"last_error"`
	LastErrorCode string     `json:"last_error_code"`
	JobStatus     string     `json:"job_status"`
}

// States reads only Nexus data. The caller supplies view-authorized accounts;
// workspace filtering also applies to scopes and their active job payloads.
func (s *Store) States(ctx context.Context, workspace int64, allowed []int64, service string) ([]State, error) {
	rows, err := s.DB.Query(ctx, `SELECT s.account_id,s.service,s.region,s.last_success_at,s.last_started_at,
 s.next_due_at,s.last_error,s.last_error_code,COALESCE((SELECT j.status FROM jobs j
 WHERE j.kind='scheduled.sync' AND j.status IN ('queued','running')
 AND j.dedupe_key='sync:'||s.account_id||':'||s.service||':'||s.region
 AND j.payload->>'workspace_id'=$1::bigint::text AND j.payload->>'account_id'=s.account_id::text
 AND j.payload->>'state_id'=s.id::text AND j.payload->>'service'=s.service AND j.payload->>'region'=s.region
 ORDER BY CASE WHEN j.status='running' THEN 0 ELSE 1 END LIMIT 1),'idle')
 FROM sync_state s JOIN cloud_accounts a ON a.id=s.account_id
 WHERE a.workspace_id=$1 AND a.id=ANY($2) AND ($3='' OR s.service=$3) AND s.enabled
 AND NOT(s.region='*' AND s.service IN ('eds.users','ecs.security_groups'))
 ORDER BY s.account_id,s.service,s.region`, workspace, allowed, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []State{}
	for rows.Next() {
		var row State
		if err := rows.Scan(&row.AccountID, &row.Service, &row.Region, &row.LastSuccessAt, &row.LastStartedAt, &row.NextDueAt, &row.LastError, &row.LastErrorCode, &row.JobStatus); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
