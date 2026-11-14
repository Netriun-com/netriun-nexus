// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// migrateServiceScopes copies legacy history before reconcile removes '*'.
// User directories inherit only snapshots from their own endpoint partition;
// an old account-wide users success never proves both directories were read.
func (s *Store) migrateServiceScopes(ctx context.Context, tx pgx.Tx, a Account, service string, regions []string) error {
	if service != SecurityGroups && service != Users && service != Desktops {
		return nil
	}
	for _, region := range regions {
		_, err := tx.Exec(ctx, `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload,fetched_at)
 SELECT workspace_id,account_id,service_key,$3,
 CASE WHEN $2 IN ('ecs.security_groups','eds.desktops') THEN jsonb_build_object('data',COALESCE((SELECT jsonb_agg(item) FROM jsonb_array_elements(COALESCE(NULLIF(payload->'data','null'::jsonb),'[]'::jsonb)) item WHERE COALESCE(item->>'region',item->>'RegionId')=$3),'[]'::jsonb),'failed_regions',payload->'failed_regions') ELSE payload END,fetched_at
 FROM service_snapshots WHERE account_id=$1 AND service_key=$2 AND
 CASE WHEN $2='ecs.security_groups' THEN region=''
 WHEN $2='eds.desktops' THEN region='all'
 ELSE region NOT IN ('*',$3) AND CASE WHEN region LIKE 'cn-%' AND region<>'cn-hongkong' THEN 'cn-shanghai' ELSE 'ap-southeast-1' END=$3 END
 ORDER BY fetched_at DESC LIMIT 1 ON CONFLICT(account_id,service_key,region)
 DO UPDATE SET payload=excluded.payload,fetched_at=excluded.fetched_at
 WHERE service_snapshots.fetched_at<excluded.fetched_at`, a.ID, service, region)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO sync_state(account_id,service,region,"interval",last_started_at,last_success_at,last_error,last_error_code,enabled,next_due_at)
 SELECT account_id,service,$3,"interval",last_started_at,
 CASE WHEN $2='eds.users' THEN (SELECT fetched_at FROM service_snapshots WHERE account_id=$1 AND service_key=$2 AND region=$3) ELSE last_success_at END,
 last_error,last_error_code,enabled,next_due_at FROM sync_state WHERE account_id=$1 AND service=$2 AND region='*'
 ON CONFLICT(account_id,service,region) DO NOTHING`, a.ID, service, region)
		if err != nil {
			return err
		}
	}
	return nil
}
