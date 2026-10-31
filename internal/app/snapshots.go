// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (a *App) saveServiceSnapshot(ctx context.Context, workspaceID, accountID int64, service, region string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,service_key,region) DO UPDATE SET payload=excluded.payload,fetched_at=now()`, workspaceID, accountID, service, region, raw)
	return err
}

func (a *App) serviceSnapshot(ctx context.Context, workspaceID, accountID int64, service, region string) (map[string]any, bool, error) {
	var raw []byte
	var fetchedAt time.Time
	err := a.DB.QueryRow(ctx, `SELECT payload,fetched_at FROM service_snapshots WHERE workspace_id=$1 AND account_id=$2 AND service_key=$3 AND region=$4`, workspaceID, accountID, service, region).Scan(&raw, &fetchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"data": []any{}, "snapshot": false, "fetched_at": nil}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	payload := map[string]any{}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, false, err
	}
	payload["snapshot"], payload["fetched_at"] = true, fetchedAt
	return payload, true, nil
}

// instanceSnapshot reads rows and scope freshness in the same database snapshot.
// The oldest successful account collection governs freshness, including empty
// inventories. A never-collected account makes the entire scope stale.
func (a *App) instanceSnapshot(ctx context.Context, workspaceID int64, ids []int64, query, state, region string, limit, offset int) (json.RawMessage, error) {
	var result json.RawMessage
	err := a.DB.QueryRow(ctx, `SELECT json_build_object(
 'data', COALESCE((SELECT json_agg(t) FROM (SELECT i.*,a.name AS account_name,a.provider,a.group_id FROM instances i JOIN cloud_accounts a ON a.id=i.account_id WHERE a.workspace_id=$1 AND a.id=ANY($2) AND ($3='' OR i.name ILIKE '%'||$3||'%' OR i.instance_id ILIKE '%'||$3||'%' OR i.public_ip ILIKE '%'||$3||'%' OR a.name ILIKE '%'||$3||'%') AND ($4='' OR i.state=$4) AND ($5='' OR i.region=$5) ORDER BY i.name,i.id LIMIT $6 OFFSET $7) t), '[]'::json),
 'fetched_at', (SELECT CASE WHEN count(*)=count(last_sync_at) THEN min(last_sync_at) END FROM cloud_accounts WHERE workspace_id=$1 AND id=ANY($2))
 )`, workspaceID, ids, query, state, region, limit, offset).Scan(&result)
	return result, err
}
