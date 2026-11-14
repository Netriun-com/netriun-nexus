// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/jobs"
)

// SaveServiceSnapshot commits a complete scope only while this job still owns
// its lease and the enabled sync_state generation. Provider failures must return
// before calling it, preserving the previous snapshot for that scope.
func (s *Store) SaveServiceSnapshot(ctx context.Context, workspace, account int64, service, region string, payload any) error {
	a, ok := ctx.Value(attemptKey{}).(attempt)
	if !ok || a.Payload.WorkspaceID != workspace || a.Payload.AccountID != account || a.Payload.Service != service || a.Payload.Region != region {
		return jobs.ErrLeaseLost
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id int64
	if err := tx.QueryRow(ctx, "SELECT id FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE", account, workspace).Scan(&id); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM sync_state WHERE id=$1 AND account_id=$2 AND service=$3 AND region=$4
 AND enabled AND last_job_id=$5 AND last_attempt=$6 FOR UPDATE`, a.Payload.StateID, account, service, region, a.Job.ID, a.Job.Attempts).Scan(&id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(account_id,service_key,region) DO UPDATE SET payload=excluded.payload,fetched_at=clock_timestamp()`, workspace, account, service, region, raw)
	if err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ServiceSnapshot combines stored regional snapshots for the unchanged list
// endpoints. It does no provider work. Legacy snapshots remain readable while
// discovery populates regional scopes after an upgrade.
func (s *Store) ServiceSnapshot(ctx context.Context, workspace, account int64, service, region string) (map[string]any, bool, error) {
	originalService := service
	aggregate := service == SecurityGroups && region == "" || service == Desktops && region == "all" || service == "eds.regions"
	if service == "eds.regions" {
		service = Desktops
	}
	if service == Users {
		region = cloud.EDSUserRegion(region)
	}
	if service == Buckets {
		region = AccountWide
	}
	rows, err := s.DB.Query(ctx, `SELECT st.region,st.last_error,ss.payload,ss.fetched_at FROM sync_state st
 JOIN cloud_accounts a ON a.id=st.account_id AND a.workspace_id=$1
 LEFT JOIN LATERAL (
 SELECT CASE WHEN (st.service='ecs.security_groups' AND region='') OR (st.service='eds.desktops' AND region='all')
 THEN jsonb_build_object('data',COALESCE((SELECT jsonb_agg(item) FROM jsonb_array_elements(COALESCE(NULLIF(payload->'data','null'::jsonb),'[]'::jsonb)) item
 WHERE COALESCE(item->>'region',item->>'RegionId')=st.region),'[]'::jsonb),'failed_regions',payload->'failed_regions') ELSE payload END AS payload,fetched_at
 FROM service_snapshots WHERE account_id=st.account_id AND workspace_id=$1 AND service_key=st.service
 AND (region=st.region OR (st.service='ecs.security_groups' AND region='') OR (st.service='eds.desktops' AND region='all') OR (st.service='oss.buckets' AND region='') OR
 (st.service='eds.users' AND region<>'*' AND CASE WHEN region LIKE 'cn-%' AND region<>'cn-hongkong' THEN 'cn-shanghai' ELSE 'ap-southeast-1' END=st.region))
 ORDER BY fetched_at DESC,(region=st.region) DESC LIMIT 1) ss ON true
 WHERE st.account_id=$2 AND st.service=$3 AND ($5 OR st.region=$4) AND NOT(st.region='*' AND st.service IN ('ecs.security_groups','eds.users')) ORDER BY st.region`, workspace, account, service, region, aggregate)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	data := []json.RawMessage{}
	failed := []string{}
	regions := []map[string]any{}
	var oldest *time.Time
	found, missing := false, false
	var single map[string]any
	for rows.Next() {
		var scope, message string
		var raw []byte
		var fetched *time.Time
		if err := rows.Scan(&scope, &message, &raw, &fetched); err != nil {
			return nil, false, err
		}
		if message != "" {
			failed = append(failed, scope)
		}
		var payload struct {
			Data []json.RawMessage `json:"data"`
		}
		if raw == nil {
			missing = true
		} else {
			found = true
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, false, err
			}
			if !aggregate {
				if err := json.Unmarshal(raw, &single); err != nil {
					return nil, false, err
				}
			}
			data = append(data, payload.Data...)
			if fetched != nil && (oldest == nil || fetched.Before(*oldest)) {
				oldest = fetched
			}
		}
		regions = append(regions, map[string]any{"id": scope, "name": scope, "desktops": len(payload.Data), "available": raw != nil && message == ""})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if missing {
		oldest = nil
	}
	if found {
		out := single
		if aggregate {
			out = map[string]any{"data": data, "failed_regions": failed}
		}
		if originalService == "eds.regions" {
			out = map[string]any{"data": regions, "total_desktops": len(data)}
		}
		out["snapshot"], out["fetched_at"] = found, oldest
		return out, found, nil
	}
	rows.Close()
	// Legacy aggregate remains readable before discovery/reconciliation has
	// populated regional snapshots. No provider call occurs on this path.
	fallbackRegion := region
	if service == Buckets || service == SecurityGroups || originalService == "eds.regions" {
		fallbackRegion = ""
	}
	if aggregate && service == Desktops && originalService != "eds.regions" {
		fallbackRegion = "all"
	}
	var raw []byte
	var fetched time.Time
	err = s.DB.QueryRow(ctx, `SELECT payload,fetched_at FROM service_snapshots WHERE workspace_id=$1 AND account_id=$2 AND service_key=$3 AND region=$4`, workspace, account, originalService, fallbackRegion).Scan(&raw, &fetched)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"data": []any{}, "snapshot": false, "fetched_at": nil}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, err
	}
	out["snapshot"], out["fetched_at"] = true, fetched
	return out, true, nil
}
