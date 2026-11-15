// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/jobs"
)

var ErrNoScopes = errors.New("No enabled sync scope is available. Check the region selection or wait for region discovery.")
var ErrAccountAccess = errors.New("Cloud account access required")

type RefreshFailure struct {
	ID      int64  `json:"id"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// Refresh preserves the legacy inventory polling contract. ID identifies a
// receipt containing durable sync job IDs, not another executable job.
type Refresh struct {
	Service        string           `json:"-"`
	Region         string           `json:"-"`
	ID             string           `json:"id"`
	AccountIDs     []int64          `json:"account_ids"`
	Status         string           `json:"status"`
	CreatedAt      time.Time        `json:"created_at"`
	StartedAt      *time.Time       `json:"started_at,omitempty"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
	DurationMS     int64            `json:"duration_ms,omitempty"`
	FailedAccounts []RefreshFailure `json:"failed_accounts"`
	Error          string           `json:"error,omitempty"`
}

// RefreshCompute queues only existing enabled scopes, never provider discovery
// on the HTTP path. The caller must authorize ids. Missing scopes fail the whole
// request atomically, allowing the UI to show a discovery/selection error.
func (s *Store) RefreshCompute(ctx context.Context, workspace int64, ids []int64, region string) (Refresh, error) {
	return s.RefreshService(ctx, workspace, ids, Compute, region)
}

func (s *Store) RefreshService(ctx context.Context, workspace int64, ids []int64, service, region string) (Refresh, error) {
	scope := region
	if scope == "all" {
		scope = ""
	}
	if service == Users {
		scope = cloud.EDSUserRegion(scope)
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Refresh{}, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT s.id,a.workspace_id,s.account_id,s.service,s.region,a.provider
        FROM sync_state s JOIN cloud_accounts a ON a.id=s.account_id
        WHERE a.workspace_id=$1 AND a.id=ANY($2) AND s.enabled AND s.service=$4 AND NOT(s.region='*' AND s.service IN ('eds.users','ecs.security_groups'))
          AND ($3='' OR s.region=$3 OR s.region='*') ORDER BY s.id`, workspace, ids, scope, service)
	if err != nil {
		return Refresh{}, err
	}
	var scopes []Payload
	seen := map[int64]bool{}
	for rows.Next() {
		var p Payload
		if err := rows.Scan(&p.StateID, &p.WorkspaceID, &p.AccountID, &p.Service, &p.Region, &p.Provider); err != nil {
			rows.Close()
			return Refresh{}, err
		}
		scopes = append(scopes, p)
		seen[p.AccountID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Refresh{}, err
	}
	if len(ids) == 0 || len(seen) != len(ids) {
		return Refresh{}, ErrNoScopes
	}
	jobIDs := make([]int64, 0, len(scopes))
	for _, p := range scopes {
		raw, _ := json.Marshal(p)
		j, err := s.Queue.EnqueueTx(ctx, tx, jobs.Params{Kind: JobKind, Payload: raw, Priority: 100, DedupeKey: p.Key(), ConcurrencyKey: fmt.Sprintf("account:%d", p.AccountID), ProviderKey: p.ProviderKey()})
		if err != nil {
			return Refresh{}, err
		}
		if err := s.Queue.PromoteTx(ctx, tx, j.ID, 100); err != nil {
			return Refresh{}, err
		}
		jobIDs = append(jobIDs, j.ID)
	}
	slices.Sort(jobIDs)
	hashRegion := region
	if service == Compute {
		hashRegion = ""
	}
	hash := sha256.Sum256([]byte(fmt.Sprint(workspace, service, hashRegion, jobIDs)))
	id := base64.RawURLEncoding.EncodeToString(hash[:])
	_, err = tx.Exec(ctx, `INSERT INTO inventory_refreshes(id,workspace_id,account_ids,job_ids,service,region) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, id, workspace, ids, jobIDs, service, region)
	if err != nil {
		return Refresh{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Refresh{}, err
	}
	return s.ServiceRefreshStatus(ctx, workspace, id, ids, service)
}

// RefreshStatus filters the receipt AND every underlying job by workspace and
// account access. It never looks up an executable job by ID alone.
func (s *Store) RefreshStatus(ctx context.Context, workspace int64, id string, allowed []int64) (Refresh, error) {
	return s.ServiceRefreshStatus(ctx, workspace, id, allowed, Compute)
}

func (s *Store) ServiceRefreshStatus(ctx context.Context, workspace int64, id string, allowed []int64, service string) (Refresh, error) {
	out := Refresh{ID: id, Status: "queued", FailedAccounts: []RefreshFailure{}}
	var jobIDs []int64
	err := s.DB.QueryRow(ctx, `SELECT account_ids,job_ids,created_at,service,region FROM inventory_refreshes WHERE id=$1 AND workspace_id=$2 AND (service=$3 OR ($3='eds' AND service IN ('eds.desktops','eds.users')))`, id, workspace, service).Scan(&out.AccountIDs, &jobIDs, &out.CreatedAt, &out.Service, &out.Region)
	if err != nil {
		return out, err
	}
	for _, account := range out.AccountIDs {
		if !slices.Contains(allowed, account) {
			return out, ErrAccountAccess
		}
	}
	rows, err := s.DB.Query(ctx, `SELECT j.status,j.attempts,j.started_at,j.updated_at,(j.payload->>'account_id')::bigint,j.last_error
        FROM jobs j JOIN cloud_accounts a ON a.id=(j.payload->>'account_id')::bigint AND a.workspace_id=$2
        WHERE j.id=ANY($1) AND j.kind=$3 AND j.payload->>'workspace_id'=$2::bigint::text
          AND j.payload->>'service'=$5 AND a.id=ANY($4) ORDER BY j.id`, jobIDs, workspace, JobKind, allowed, out.Service)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	count, active, failed := 0, false, false
	failedAccounts := map[int64]bool{}
	var completed time.Time
	for rows.Next() {
		var status, message string
		var attempts int
		var started *time.Time
		var updated time.Time
		var account int64
		if err := rows.Scan(&status, &attempts, &started, &updated, &account, &message); err != nil {
			return out, err
		}
		count++
		if started != nil && (out.StartedAt == nil || started.Before(*out.StartedAt)) {
			out.StartedAt = started
		}
		if updated.After(completed) {
			completed = updated
		}
		if status == "queued" || status == "running" {
			active = true
			if attempts > 0 {
				out.Status = "running"
			}
		}
		if status == "failed" {
			failed = true
			if !failedAccounts[account] {
				code, safe := SafeStoredError(message)
				out.FailedAccounts = append(out.FailedAccounts, RefreshFailure{ID: account, Message: safe, Code: code})
				failedAccounts[account] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if count != len(jobIDs) {
		return out, pgx.ErrNoRows
	}
	if !active {
		out.Status = "completed"
		if failed {
			out.Status = "partial"
		}
		out.CompletedAt = &completed
		if out.StartedAt != nil {
			out.DurationMS = completed.Sub(*out.StartedAt).Milliseconds()
		}
	}
	return out, nil
}

func (s *Store) CleanupRefreshes(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM inventory_refreshes WHERE id IN (
        SELECT r.id FROM inventory_refreshes r WHERE
        NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=ANY(r.job_ids) AND j.status IN ('queued','running'))
        AND EXISTS(SELECT 1 FROM unnest(r.job_ids) AS requested(job_id) WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE jobs.id=requested.job_id))
        ORDER BY r.created_at LIMIT 500)`)
	return err
}
