// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

// ReconcileAllRegions reconciles a complete account-wide collection, preserving
// each object's real region. Previously stored regions absent from items are
// reconciled empty, so disappeared regions are cleared too. Only this workspace,
// account, service and kind are affected. The caller owns the transaction and
// must fence its lease. Never call this after a partial or failed collection.
func ReconcileAllRegions(ctx context.Context, tx pgx.Tx, scope Scope, items map[string][]Item, relations map[string][]Relation) error {
	if tx == nil || scope.WorkspaceID <= 0 || scope.AccountID <= 0 || scope.Provider == "" || scope.Service == "" || scope.Kind == "" || scope.Region != "*" {
		return ErrInvalid
	}
	// Lock before enumerating old regions, using Reconcile's account-first order.
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM cloud_accounts WHERE workspace_id=$1 AND id=$2 AND provider=$3 FOR UPDATE`, scope.WorkspaceID, scope.AccountID, scope.Provider).Scan(&id); err != nil {
		return err
	}
	regions := map[string]bool{}
	for region := range items {
		if region == "" || region == "*" {
			return ErrInvalid
		}
		regions[region] = true
	}
	for region := range relations {
		if !regions[region] {
			return ErrInvalid
		}
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT region FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service=$3 AND kind=$4`, scope.WorkspaceID, scope.AccountID, scope.Service, scope.Kind)
	if err != nil {
		return err
	}
	for rows.Next() {
		var region string
		if err := rows.Scan(&region); err != nil {
			rows.Close()
			return err
		}
		regions[region] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(regions))
	for region := range regions {
		ordered = append(ordered, region)
	}
	slices.Sort(ordered)
	for _, region := range ordered {
		scope.Region = region
		if err := Reconcile(ctx, tx, scope, items[region], relations[region]); err != nil {
			return err
		}
	}
	return nil
}
