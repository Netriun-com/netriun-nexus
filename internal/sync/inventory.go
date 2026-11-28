// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/resources"
)

type attemptKey struct{}
type attempt struct {
	Payload Payload
	Job     jobs.Job
}

// fenceInventory locks the account and current lease before changing inventory.
// A provider response from an expired attempt cannot replace newer data.
func (s *Store) fenceInventory(ctx context.Context, tx pgx.Tx, account int64, whole bool, region string) (string, error) {
	a, ok := ctx.Value(attemptKey{}).(attempt)
	if !ok || a.Payload.Service != Compute || a.Payload.AccountID != account || whole != (a.Payload.Region == AccountWide) || (!whole && region != a.Payload.Region) {
		return "", jobs.ErrLeaseLost
	}
	var id int64
	var provider string
	if err := tx.QueryRow(ctx, "SELECT provider FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE", account, a.Payload.WorkspaceID).Scan(&provider); err != nil {
		return "", err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return "", err
	}
	err := tx.QueryRow(ctx, `SELECT id FROM sync_state WHERE id=$1 AND account_id=$2 AND service='compute' AND region=$3
   AND enabled AND last_job_id=$4 AND last_attempt=$5 FOR UPDATE`, a.Payload.StateID, account, a.Payload.Region, a.Job.ID, a.Job.Attempts).Scan(&id)
	return provider, err
}

// ReconcileInventory preserves the existing upsert/delete semantics, with writes
// fenced by the job attempt carried in the context supplied by Handler.
func (s *Store) ReconcileInventory(ctx context.Context, accountID int64, items []providers.Resource, relations []providers.Relation, wholeAccount bool, region string) error {
	// Reconcile only after every provider page succeeded; failed regions retain their inventory.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	provider, err := s.fenceInventory(ctx, tx, accountID, wholeAccount, region)
	if err != nil {
		return err
	}
	a := ctx.Value(attemptKey{}).(attempt)
	scope := resources.Scope{WorkspaceID: a.Payload.WorkspaceID, AccountID: accountID, Provider: provider, Service: Compute, Kind: providers.Instance, Region: region}
	catalog := map[string][]resources.Item{}
	edges := map[string][]resources.Relation{}
	for _, i := range items {
		if i.WorkspaceID != scope.WorkspaceID || i.AccountID != accountID || i.Provider != provider || i.Service != Compute || i.Kind != providers.Instance {
			return errors.New("provider returned an instance outside the sync scope")
		}
		if !wholeAccount && i.Region != region {
			return errors.New("provider returned an instance outside the sync region")
		}
		if i.Region == "" || i.Region == AccountWide {
			return errors.New("provider returned an instance without a region")
		}
		b, err := json.Marshal(i.Details)
		if err != nil {
			return err
		}
		tags, err := json.Marshal(i.Tags)
		if err != nil {
			return err
		}
		summary, err := json.Marshal(i.Summary)
		if err != nil {
			return err
		}
		catalog[i.Region] = append(catalog[i.Region], resources.Item{ProviderID: i.ProviderID, Name: i.Name, State: i.State, Tags: tags, Summary: summary, Details: b})

	}
	for _, edge := range relations {
		if !wholeAccount && edge.From.Region != region {
			return resources.ErrInvalid
		}
		edges[edge.From.Region] = append(edges[edge.From.Region], resources.Relation{From: resources.Reference(edge.From), To: resources.Reference(edge.To), Relation: edge.Relation})
	}
	if wholeAccount {
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
