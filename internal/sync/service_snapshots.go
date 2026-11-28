// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"

	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/resources"
)

// SaveServiceSnapshot reconciles the resource catalog,
// only while this job owns its lease and the enabled sync_state generation.
// Provider failures must return before calling it, preserving the previous inventory.
func (s *Store) SaveServiceSnapshot(ctx context.Context, workspace, account int64, service, region string, items []providers.Resource, relations []providers.Relation) error {
	a, ok := ctx.Value(attemptKey{}).(attempt)
	if !ok || a.Payload.WorkspaceID != workspace || a.Payload.AccountID != account || a.Payload.Service != service || a.Payload.Region != region {
		return jobs.ErrLeaseLost
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id int64
	var provider string
	if err := tx.QueryRow(ctx, "SELECT provider FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE", account, workspace).Scan(&provider); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM sync_state WHERE id=$1 AND account_id=$2 AND service=$3 AND region=$4
 AND enabled AND last_job_id=$5 AND last_attempt=$6 FOR UPDATE`, a.Payload.StateID, account, service, region, a.Job.ID, a.Job.Attempts).Scan(&id); err != nil {
		return err
	}
	scope, catalog, edges, err := serviceCatalog(workspace, account, provider, service, region, items, relations)
	if err != nil {
		return err
	}
	if service == Buckets {
		err = resources.ReconcileAllRegions(ctx, tx, scope, catalog, edges)
	} else {
		err = resources.Reconcile(ctx, tx, scope, catalog[region], edges[region])
	}
	if err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ServiceSnapshot is the scheduler-side read for one explicit account scope.
// HTTP callers must pass their Policy-accessible account set to resources directly.
func (s *Store) ServiceSnapshot(ctx context.Context, workspace, account int64, service, region string) (map[string]any, bool, error) {
	return resources.New(s.DB).ServiceSnapshot(ctx, workspace, []int64{account}, service, region)
}
