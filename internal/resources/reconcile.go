// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Reconcile never starts or commits a transaction. An empty complete collection
// deletes the scope; errors require the caller to roll back the transaction.
func Reconcile(ctx context.Context, tx pgx.Tx, scope Scope, items []Item, relations []Relation) error {
	if tx == nil || scope.WorkspaceID <= 0 || scope.AccountID <= 0 || scope.Provider == "" || scope.Service == "" || scope.Region == "" || scope.Kind == "" {
		return ErrInvalid
	}
	ids := make([]string, 0, len(items))
	normalized := make([]Item, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item.ProviderID == "" || seen[item.ProviderID] {
			return fmt.Errorf("%w: empty or duplicate provider ID", ErrInvalid)
		}
		seen[item.ProviderID] = true
		ids = append(ids, item.ProviderID)
		for _, value := range []*json.RawMessage{&item.Tags, &item.Summary, &item.Details} {
			if len(*value) == 0 {
				*value = json.RawMessage(`{}`)
			}
			if !json.Valid(*value) {
				return fmt.Errorf("%w: malformed JSON", ErrInvalid)
			}
		}
		normalized = append(normalized, item)
	}
	for _, edge := range relations {
		if edge.Relation == "" || edge.From.Service != scope.Service || edge.From.Region != scope.Region || edge.From.Kind != scope.Kind || !seen[edge.From.ProviderID] || edge.To.Service == "" || edge.To.Region == "" || edge.To.Kind == "" || edge.To.ProviderID == "" {
			return fmt.Errorf("%w: relation outside source scope", ErrInvalid)
		}
	}
	// Serialize account reconciliations, using the same account-first lock order
	// as existing sync writers. The caller supplies the lease fence, not this store.
	var account int64
	if err := tx.QueryRow(ctx, `SELECT id FROM cloud_accounts WHERE workspace_id=$1 AND id=$2 AND provider=$3 FOR UPDATE`, scope.WorkspaceID, scope.AccountID, scope.Provider).Scan(&account); err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO resources(workspace_id,account_id,provider,service,region,kind,provider_id,name,state,tags,summary,details)
 SELECT $1,$2,$3,$4,$5,$6,i->>'provider_id',i->>'name',i->>'state',i->'tags',i->'summary',i->'details'
 FROM jsonb_array_elements($7::jsonb) i
 ON CONFLICT(account_id,service,region,kind,provider_id) DO UPDATE SET
 provider=excluded.provider,name=excluded.name,state=excluded.state,tags=excluded.tags,summary=excluded.summary,details=excluded.details,
 seen_at=clock_timestamp(),
 version=resources.version+CASE WHEN (resources.name,resources.state,resources.tags,resources.summary,resources.details)
 IS DISTINCT FROM (excluded.name,excluded.state,excluded.tags,excluded.summary,excluded.details) THEN 1 ELSE 0 END,
 updated_at=CASE WHEN (resources.name,resources.state,resources.tags,resources.summary,resources.details)
 IS DISTINCT FROM (excluded.name,excluded.state,excluded.tags,excluded.summary,excluded.details) THEN clock_timestamp() ELSE resources.updated_at END
 WHERE resources.workspace_id=$1 AND resources.account_id=$2`, scope.WorkspaceID, scope.AccountID, scope.Provider, scope.Service, scope.Region, scope.Kind, raw)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service=$3 AND region=$4 AND kind=$5 AND NOT(provider_id=ANY($6))`, scope.WorkspaceID, scope.AccountID, scope.Service, scope.Region, scope.Kind, ids); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM resource_relations rr USING resources r WHERE rr.from_id=r.id AND r.workspace_id=$1 AND r.account_id=$2 AND r.service=$3 AND r.region=$4 AND r.kind=$5 AND rr.owner_service IN ('',$3)`, scope.WorkspaceID, scope.AccountID, scope.Service, scope.Region, scope.Kind); err != nil {
		return err
	}
	if len(relations) == 0 {
		return nil
	}
	raw, err = json.Marshal(relations)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO resource_relations(from_id,to_id,relation,owner_service)
 SELECT src.id,dst.id,e->>'relation',$3 FROM jsonb_array_elements($6::jsonb) e
 JOIN resources src ON src.workspace_id=$1 AND src.account_id=$2 AND src.service=$3 AND src.region=$4 AND src.kind=$5 AND src.provider_id=e->'from'->>'provider_id'
 JOIN resources dst ON dst.workspace_id=$1 AND dst.account_id=$2 AND dst.service=e->'to'->>'service' AND dst.region=e->'to'->>'region' AND dst.kind=e->'to'->>'kind' AND dst.provider_id=e->'to'->>'provider_id'
 ON CONFLICT DO NOTHING`, scope.WorkspaceID, scope.AccountID, scope.Service, scope.Region, scope.Kind, raw)
	return err
}
