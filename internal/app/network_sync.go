// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"github.com/netriun/nexus/internal/providers"
	syncer "github.com/netriun/nexus/internal/sync"
)

func (a *App) collectNetwork(ctx context.Context, p syncer.Payload, account syncer.Account) error {
	adapter, err := a.Providers.Lookup(account.Provider)
	if err != nil {
		return err
	}
	items, edges, err := adapter.List(ctx, providers.Account{WorkspaceID: p.WorkspaceID, ID: p.AccountID, Provider: account.Provider, Region: p.Region}, syncer.Network, p.Region)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.Sync.ReconcileNetwork(ctx, p, items, edges)
}
