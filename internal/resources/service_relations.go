// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// ReconcileServiceRelations replaces edges owned by a complete regional service
// collection, including edges whose source is another service. The caller has
// locked the account and fenced the lease in this same transaction. Missing
// endpoints are skipped; both ends resolve only within this workspace/account.
func ReconcileServiceRelations(ctx context.Context, tx pgx.Tx, scope Scope, edges []Relation) error {
	if tx == nil || scope.WorkspaceID <= 0 || scope.AccountID <= 0 || scope.Service == "" || scope.Region == "" || scope.Region == "*" {
		return ErrInvalid
	}
	for _, edge := range edges {
		if edge.Relation == "" || edge.From.Service == "" || edge.From.Kind == "" || edge.From.ProviderID == "" || edge.To.Service == "" || edge.To.Kind == "" || edge.To.ProviderID == "" || edge.From.Region != scope.Region || edge.To.Region != scope.Region {
			return ErrInvalid
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_relations rr USING resources r WHERE rr.from_id=r.id AND r.workspace_id=$1 AND r.account_id=$2 AND r.region=$3 AND rr.owner_service=$4`, scope.WorkspaceID, scope.AccountID, scope.Region, scope.Service); err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	raw, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO resource_relations(from_id,to_id,relation,owner_service)
 SELECT src.id,dst.id,e->>'relation',$4 FROM jsonb_array_elements($5::jsonb) e
 JOIN resources src ON src.workspace_id=$1 AND src.account_id=$2 AND src.region=$3 AND src.service=e->'from'->>'service' AND src.kind=e->'from'->>'kind' AND src.provider_id=e->'from'->>'provider_id'
 JOIN resources dst ON dst.workspace_id=$1 AND dst.account_id=$2 AND dst.region=$3 AND dst.service=e->'to'->>'service' AND dst.kind=e->'to'->>'kind' AND dst.provider_id=e->'to'->>'provider_id'
 ON CONFLICT DO NOTHING`, scope.WorkspaceID, scope.AccountID, scope.Region, scope.Service, raw)
	return err
}
