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
	// Use the same account-first serialization as resource reconciliation.
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM cloud_accounts WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, scope.WorkspaceID, scope.AccountID).Scan(&id); err != nil {
		return err
	}
	return reconcileRelations(ctx, tx, scope, edges, true)
}

// Compare resolved edge sets instead of deleting/reinserting unchanged edges.
// This avoids false change events and preserves another service's ownership.
func reconcileRelations(ctx context.Context, tx pgx.Tx, scope Scope, edges []Relation, wholeService bool) error {
	if edges == nil {
		edges = []Relation{}
	}
	raw, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	const desired = `WITH desired AS MATERIALIZED (
 SELECT DISTINCT src.id AS from_id,dst.id AS to_id,e->>'relation' AS relation
 FROM jsonb_array_elements($7::jsonb) e
 JOIN resources src ON src.workspace_id=$1 AND src.account_id=$2 AND src.region=$3
   AND src.service=e->'from'->>'service' AND src.kind=e->'from'->>'kind' AND src.provider_id=e->'from'->>'provider_id'
 JOIN resources dst ON dst.workspace_id=$1 AND dst.account_id=$2 AND dst.region=e->'to'->>'region'
   AND dst.service=e->'to'->>'service' AND dst.kind=e->'to'->>'kind' AND dst.provider_id=e->'to'->>'provider_id'
 WHERE ($6 OR (src.service=$4 AND src.kind=$5))
) `
	args := []any{scope.WorkspaceID, scope.AccountID, scope.Region, scope.Service, scope.Kind, wholeService, raw}
	if _, err := tx.Exec(ctx, desired+`DELETE FROM resource_relations rr USING resources r
 WHERE rr.from_id=r.id AND r.workspace_id=$1 AND r.account_id=$2 AND r.region=$3
 AND (($6 AND rr.owner_service=$4) OR (NOT $6 AND r.service=$4 AND r.kind=$5 AND rr.owner_service IN ('',$4)))
 AND NOT EXISTS(SELECT 1 FROM desired d WHERE d.from_id=rr.from_id AND d.to_id=rr.to_id AND d.relation=rr.relation)`, args...); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, desired+`INSERT INTO resource_relations(from_id,to_id,relation,owner_service)
 SELECT from_id,to_id,relation,$4 FROM desired ON CONFLICT DO NOTHING`, args...)
	return err
}
