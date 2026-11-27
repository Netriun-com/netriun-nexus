// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
)

// Compatibility projections keep the existing list/detail response contracts.
// Their numeric IDs now identify catalog rows, never deprecated inventory rows.
const instanceColumns = `r.id,r.account_id,r.provider_id AS instance_id,r.region,r.name,r.state,
 COALESCE(r.summary->>'instance_type','') AS instance_type,COALESCE(r.summary->>'public_ip','') AS public_ip,
 COALESCE(r.summary->>'private_ip','') AS private_ip,r.details,r.seen_at,a.name AS account_name,a.provider`

func (s *Store) Instances(ctx context.Context, workspace int64, accounts []int64, query, state, region string, limit, offset int) (json.RawMessage, error) {
	queryText, args := instanceListQuery(workspace, accounts, query, state, region, limit, offset)
	var result json.RawMessage
	err := s.db.QueryRow(ctx, queryText, args...).Scan(&result)
	return result, err
}

func instanceListQuery(workspace int64, accounts []int64, query, state, region string, limit, offset int) (string, []any) {
	args := []any{workspace, accounts}
	predicate := `r.workspace_id=$1 AND r.account_id=ANY($2) AND r.service='compute' AND r.kind='instance'`
	// Keep name/provider-id predicates directly on the pg_trgm-indexed columns.
	// IP/account matches use an authorized candidate set, so they do not turn the
	// indexed resource search into an OR with an unindexed JSON expression.
	if query != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
		args = append(args, pattern)
		predicate += ` AND r.id IN (SELECT id FROM resources WHERE workspace_id=$1 AND account_id=ANY($2) AND (name ILIKE $3 OR provider_id ILIKE $3)
 UNION SELECT ip.id FROM resources ip WHERE ip.workspace_id=$1 AND ip.account_id=ANY($2) AND ip.service='compute' AND ip.kind='instance' AND ip.summary->>'public_ip' ILIKE $3
 UNION SELECT ar.id FROM resources ar JOIN cloud_accounts ac ON ac.id=ar.account_id AND ac.workspace_id=ar.workspace_id WHERE ar.workspace_id=$1 AND ar.account_id=ANY($2) AND ar.service='compute' AND ar.kind='instance' AND ac.name ILIKE $3)`
	}
	// Parameters after the optional search are numbered once, without interpolating input.
	n := len(args)
	args = append(args, state, region, limit, offset)
	predicate += fmt.Sprintf(" AND ($%d='' OR r.state=$%d) AND ($%d='' OR r.region=$%d)", n+1, n+1, n+2, n+2)
	queryText := `SELECT json_build_object('data',COALESCE((SELECT json_agg(t) FROM (SELECT ` + instanceColumns + `,a.group_id FROM resources r JOIN cloud_accounts a ON a.id=r.account_id AND a.workspace_id=r.workspace_id AND a.provider=r.provider WHERE ` + predicate + ` ORDER BY r.name,r.id LIMIT $` + strconv.Itoa(n+3) + ` OFFSET $` + strconv.Itoa(n+4) + `) t),'[]'::json),
 'fetched_at',(SELECT CASE WHEN count(*)=count(last_sync_at) THEN min(last_sync_at) END FROM cloud_accounts WHERE workspace_id=$1 AND id=ANY($2)))`
	return queryText, args
}

func (s *Store) Instance(ctx context.Context, workspace int64, accounts []int64, id int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.db.QueryRow(ctx, `SELECT row_to_json(t) FROM (SELECT `+instanceColumns+` FROM resources r JOIN cloud_accounts a ON a.id=r.account_id AND a.workspace_id=r.workspace_id AND a.provider=r.provider WHERE r.workspace_id=$1 AND r.account_id=ANY($2) AND r.id=$3 AND r.service='compute' AND r.kind='instance') t`, workspace, accounts, id).Scan(&raw)
	return raw, err
}

func (s *Store) Summary(ctx context.Context, workspace int64, accounts []int64) (map[string]int, error) {
	out := map[string]int{}
	var instances, running, count, regions int
	err := s.db.QueryRow(ctx, `SELECT count(r.id),count(r.id) FILTER(WHERE r.state='running'),count(DISTINCT a.id),count(DISTINCT r.region) FROM cloud_accounts a LEFT JOIN resources r ON r.account_id=a.id AND r.workspace_id=$1 AND r.account_id=ANY($2) AND r.service='compute' AND r.kind='instance' AND r.provider=a.provider WHERE a.workspace_id=$1 AND a.id=ANY($2)`, workspace, accounts).Scan(&instances, &running, &count, &regions)
	out["instances"], out["running"], out["accounts"], out["regions"] = instances, running, count, regions
	return out, err
}

// Report queries return aggregated catalog rows to the existing presentation code.
func (s *Store) ReportAccounts(ctx context.Context, workspace int64, accounts []int64) (pgx.Rows, error) {
	return s.db.Query(ctx, `SELECT a.id,a.name,a.provider,count(r.id),count(r.id) FILTER(WHERE lower(r.state)='running') FROM cloud_accounts a LEFT JOIN resources r ON r.account_id=a.id AND r.workspace_id=$1 AND r.account_id=ANY($2) AND r.service='compute' AND r.kind='instance' AND r.provider=a.provider WHERE a.workspace_id=$1 AND a.id=ANY($2) GROUP BY a.id ORDER BY a.provider,a.name`, workspace, accounts)
}
func (s *Store) ReportInstances(ctx context.Context, workspace int64, accounts []int64) (pgx.Rows, error) {
	return s.db.Query(ctx, `SELECT region,lower(state),count(*),max(seen_at) FROM resources r WHERE workspace_id=$1 AND account_id=ANY($2) AND service='compute' AND kind='instance' AND EXISTS(SELECT 1 FROM cloud_accounts a WHERE a.workspace_id=$1 AND a.id=r.account_id AND a.provider=r.provider) GROUP BY region,lower(state) ORDER BY region,lower(state)`, workspace, accounts)
}
func (s *Store) ReportServices(ctx context.Context, workspace int64, accounts []int64) (pgx.Rows, error) {
	return s.db.Query(ctx, `WITH items AS (
 SELECT DISTINCT ON(r.account_id,r.service,r.provider_id) r.account_id,r.service,r.region,r.details,r.seen_at
 FROM resources r JOIN cloud_accounts a ON a.id=r.account_id AND a.workspace_id=r.workspace_id AND a.provider=r.provider
 WHERE r.workspace_id=$1 AND r.account_id=ANY($2) AND ((r.service='eds.desktops' AND r.kind='desktop') OR (r.service='oss.buckets' AND r.kind='bucket')) ORDER BY r.account_id,r.service,r.provider_id,r.region
 ) SELECT account_id,service,region,COALESCE(NULLIF(details->>'ChargeType',''),'Unknown'),count(*),
 COALESCE(sum(CASE WHEN jsonb_typeof(details->'storage_bytes')='number' THEN (details->>'storage_bytes')::numeric ELSE 0 END),0)::bigint,
 COALESCE(sum(CASE WHEN jsonb_typeof(details->'object_count')='number' THEN (details->>'object_count')::numeric ELSE 0 END),0)::bigint,max(seen_at)
 FROM items GROUP BY account_id,service,region,4 ORDER BY account_id,service,region,4`, workspace, accounts)
}

// ServiceSnapshot rebuilds the historical envelopes from native details. An
// empty successful scope is represented by sync_state, not a synthetic resource.
// Missing scopes make the aggregate stale; failed scopes retain their last data.
func (s *Store) ServiceSnapshot(ctx context.Context, workspace int64, accounts []int64, service, region string) (map[string]any, bool, error) {
	original := service
	aggregate := service == "ecs.security_groups" && region == "" || service == "eds.desktops" && region == "all" || service == "eds.regions"
	kind := ""
	switch service {
	case "eds.regions":
		service, kind = "eds.desktops", "desktop"
	case "eds.desktops":
		kind = "desktop"
	case "eds.users":
		kind = "directory_user"
		region = cloud.EDSUserRegion(region)
	case "oss.buckets":
		kind = "bucket"
		region = "*"
	case "ecs.security_groups":
		kind = "security_group"
	default:
		return nil, false, ErrInvalid
	}
	// Scan scope metadata and native details in one snapshot, including catalog
	// rows imported before discovery has created the corresponding sync_state.
	rows, err := s.db.Query(ctx, `WITH scopes AS (
 SELECT st.account_id,st.region,st.last_error,st.last_success_at FROM sync_state st JOIN cloud_accounts a ON a.id=st.account_id AND a.workspace_id=$1 WHERE a.id=ANY($2) AND st.service=$3 AND ($6 OR st.region=$4) AND st.region<>'*'
 UNION ALL SELECT a.id,'*',st.last_error,st.last_success_at FROM cloud_accounts a JOIN sync_state st ON st.account_id=a.id AND st.service=$3 AND st.region='*' WHERE a.workspace_id=$1 AND a.id=ANY($2) AND $4='*'
 ), data AS (
 SELECT account_id,CASE WHEN $4='*' THEN '*' ELSE region END AS region,jsonb_agg(details ORDER BY name,id) FILTER(WHERE NOT $7::boolean) AS items,count(*) AS item_count,min(seen_at) AS seen FROM resources r WHERE workspace_id=$1 AND account_id=ANY($2) AND service=$3 AND kind=$5 AND EXISTS(SELECT 1 FROM cloud_accounts a WHERE a.workspace_id=$1 AND a.id=r.account_id AND a.provider=r.provider) AND ($6 OR $4='*' OR region=$4) GROUP BY account_id,CASE WHEN $4='*' THEN '*' ELSE region END
 ) SELECT COALESCE(s.region,d.region),COALESCE(s.last_error,''),COALESCE(s.last_success_at,d.seen),COALESCE(d.items,'[]'::jsonb),COALESCE(d.item_count,0) FROM scopes s FULL JOIN data d ON d.account_id=s.account_id AND d.region=s.region ORDER BY 1`, workspace, accounts, service, region, kind, aggregate, original == "eds.regions")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	data := []byte{'['}
	total := 0
	failed := []string{}
	regions := []map[string]any{}
	var oldest *time.Time
	found, missing := false, false
	for rows.Next() {
		var scope, message string
		var fetched *time.Time
		var raw []byte
		var count int
		if err := rows.Scan(&scope, &message, &fetched, &raw, &count); err != nil {
			return nil, false, err
		}
		// PostgreSQL supplies complete JSON arrays. Join their interiors without
		// decoding and allocating every native resource again in Go. Counts are
		// computed by SQL, including the count-only EDS regions projection.
		if len(raw) > 2 {
			if len(data) > 1 {
				data = append(data, ',')
			}
			data = append(data, raw[1:len(raw)-1]...)
		}
		total += count
		if message != "" {
			failed = append(failed, scope)
		}
		if fetched == nil {
			missing = true
		} else {
			found = true
			if oldest == nil || fetched.Before(*oldest) {
				oldest = fetched
			}
		}
		regions = append(regions, map[string]any{"id": scope, "name": scope, "desktops": count, "available": fetched != nil && message == ""})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if missing {
		oldest = nil
	}
	if !found {
		return map[string]any{"data": []any{}, "snapshot": false, "fetched_at": nil}, false, nil
	}
	data = append(data, ']')
	out := map[string]any{"data": json.RawMessage(data), "snapshot": found, "fetched_at": oldest}
	if service == "eds.desktops" || service == "ecs.security_groups" {
		out["failed_regions"] = failed
	}
	if service == "oss.buckets" {
		out["failed_buckets"] = []string{}
	}
	if original == "eds.regions" {
		out["data"], out["total_desktops"] = regions, total
	}
	return out, found, nil
}

// AccountsWithInventory exposes inventory only for view-authorized accounts;
// account managers can still see the account configuration without its inventory.
func (s *Store) AccountsWithInventory(ctx context.Context, workspace int64, accounts, managed, viewable []int64) (pgx.Rows, error) {
	return s.db.Query(ctx, `SELECT row_to_json(t) FROM (SELECT a.id,a.name,a.provider,a.owner,a.group_id,g.name AS group_name,a.regions,ARRAY(SELECT DISTINCT i.region FROM resources i WHERE i.workspace_id=$1 AND i.account_id=ANY($4) AND i.account_id=a.id AND i.provider=a.provider AND i.service='compute' AND i.kind='instance' ORDER BY i.region) AS discovered_regions,a.last_sync_at,a.sync_error,a.sync_error_code,a.created_at,(SELECT count(*) FROM resources i WHERE i.workspace_id=$1 AND i.account_id=ANY($4) AND i.account_id=a.id AND i.provider=a.provider AND i.service='compute' AND i.kind='instance') AS instance_count,a.id=ANY($3) AS can_manage FROM cloud_accounts a LEFT JOIN access_groups g ON g.id=a.group_id WHERE a.workspace_id=$1 AND a.id=ANY($2) ORDER BY a.name) t`, workspace, accounts, managed, viewable)
}

// ComputeSeedRegion is used after the caller has authorized the explicit account
// (or by a worker holding that account scope). It never reads legacy inventory.
func (s *Store) ComputeSeedRegion(ctx context.Context, workspace int64, accounts []int64) (string, error) {
	var region string
	err := s.db.QueryRow(ctx, `SELECT COALESCE((SELECT min(r.region) FROM resources r WHERE r.workspace_id=$1 AND r.account_id=ANY($2) AND r.account_id=a.id AND r.provider=a.provider AND r.service='compute' AND r.kind='instance'),NULLIF(a.regions[1],''),'cn-hangzhou') FROM cloud_accounts a WHERE a.workspace_id=$1 AND a.id=ANY($2) ORDER BY a.id LIMIT 1`, workspace, accounts).Scan(&region)
	return region, err
}

// ServiceFreshness includes successful empty collections, which have no catalog
// row on which to store seen_at. Inventory counts still come solely from resources.
func (s *Store) ServiceFreshness(ctx context.Context, workspace int64, accounts []int64) (*time.Time, error) {
	var seen *time.Time
	err := s.db.QueryRow(ctx, `SELECT max(st.last_success_at) FROM sync_state st JOIN cloud_accounts a ON a.id=st.account_id WHERE a.workspace_id=$1 AND a.id=ANY($2) AND st.service IN ('eds.desktops','oss.buckets')`, workspace, accounts).Scan(&seen)
	return seen, err
}
