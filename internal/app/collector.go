// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/netriun/nexus/internal/providers"
)

// Azure and GCP expose subscription/project-wide compute inventory endpoints.
// Fetch once, then retain today's configured-region filter for both tables.
func (a *App) collectGlobal(ctx context.Context, workspaceID, accountID int64, provider string, regions []string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return a.collectCompute(requestCtx, workspaceID, accountID, provider, "*", regions)
}

func (a *App) collectRegion(ctx context.Context, workspaceID, accountID int64, provider, region string) error {
	regionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return a.collectCompute(regionCtx, workspaceID, accountID, provider, region, nil)
}

func (a *App) collectCompute(ctx context.Context, workspaceID, accountID int64, provider, region string, regions []string) error {
	if a.Providers == nil {
		return errors.New("compute provider registry is not initialized")
	}
	adapter, err := a.Providers.Lookup(provider)
	if err != nil {
		return err
	}
	items, relations, err := adapter.List(ctx, providers.Account{WorkspaceID: workspaceID, ID: accountID, Provider: provider, Region: region}, providers.Compute, region)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	whole := region == "*"
	if whole && len(regions) > 0 {
		items = slices.DeleteFunc(items, func(item providers.Resource) bool { return !slices.Contains(regions, item.Region) })
		relations = slices.DeleteFunc(relations, func(edge providers.Relation) bool { return !slices.Contains(regions, edge.From.Region) })
	}
	return a.Sync.ReconcileInventory(ctx, accountID, items, relations, whole, region)
}
