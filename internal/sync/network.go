// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"

	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/resources"
)

// ReconcileNetwork persists a complete region, all three kinds and their edges,
// using the account -> job -> sync-state lock order and final lease check.
func (s *Store) ReconcileNetwork(ctx context.Context, p Payload, items []providers.Resource, relations []providers.Relation) error {
	a, ok := ctx.Value(attemptKey{}).(attempt)
	if !ok || a.Payload != p || p.Service != Network || p.Region == "" || p.Region == AccountWide {
		return jobs.ErrLeaseLost
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var provider string
	if err := tx.QueryRow(ctx, `SELECT provider FROM cloud_accounts WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, p.WorkspaceID, p.AccountID).Scan(&provider); err != nil {
		return err
	}
	if provider != "aws" && provider != "alibaba" {
		return resources.ErrInvalid
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM sync_state WHERE id=$1 AND account_id=$2 AND service='network' AND region=$3 AND enabled AND last_job_id=$4 AND last_attempt=$5 FOR UPDATE`, p.StateID, p.AccountID, p.Region, a.Job.ID, a.Job.Attempts).Scan(&id); err != nil {
		return err
	}
	grouped := map[string][]resources.Item{}
	for _, item := range items {
		if item.WorkspaceID != p.WorkspaceID || item.AccountID != p.AccountID || item.Provider != provider || item.Service != Network || item.Region != p.Region {
			return resources.ErrInvalid
		}
		switch item.Kind {
		case "network", "subnet", "load_balancer":
		default:
			return resources.ErrInvalid
		}
		tags, err := json.Marshal(item.Tags)
		if err != nil {
			return err
		}
		summary, err := json.Marshal(item.Summary)
		if err != nil {
			return err
		}
		details, err := json.Marshal(item.Details)
		if err != nil {
			return err
		}
		grouped[item.Kind] = append(grouped[item.Kind], resources.Item{ProviderID: item.ProviderID, Name: item.Name, State: item.State, Tags: tags, Summary: summary, Details: details})
	}
	scope := resources.Scope{WorkspaceID: p.WorkspaceID, AccountID: p.AccountID, Provider: provider, Service: Network, Region: p.Region}
	for _, kind := range []string{"network", "subnet", "load_balancer"} {
		scope.Kind = kind
		if err := resources.Reconcile(ctx, tx, scope, grouped[kind], nil); err != nil {
			return err
		}
	}
	edges := make([]resources.Relation, 0, len(relations))
	for _, edge := range relations {
		edges = append(edges, resources.Relation{From: resources.Reference(edge.From), To: resources.Reference(edge.To), Relation: edge.Relation})
	}
	if err := resources.ReconcileServiceRelations(ctx, tx, scope, edges); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
