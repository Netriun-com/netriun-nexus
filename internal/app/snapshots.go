// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"github.com/netriun/nexus/internal/resources"
	"slices"
)

func (a *App) serviceSnapshot(ctx context.Context, workspaceID, accountID int64, service, region string) (map[string]any, bool, error) {
	u, ok := ctx.Value(userKey).(User)
	if !ok || u.WorkspaceID != workspaceID {
		return nil, false, errForbidden
	}
	ids, err := a.Policy.AccessibleAccountIDs(ctx, u, CapabilityAccountView)
	if err != nil {
		return nil, false, err
	}
	if !slices.Contains(ids, accountID) {
		return nil, false, errForbidden
	}
	return resources.New(a.DB).ServiceSnapshot(ctx, workspaceID, []int64{accountID}, service, region)
}

// instanceSnapshot keeps the historical offset-based list response.
func (a *App) instanceSnapshot(ctx context.Context, workspaceID int64, ids []int64, query, state, region string, limit, offset int) (json.RawMessage, error) {
	return resources.New(a.DB).Instances(ctx, workspaceID, ids, query, state, region, limit, offset)
}
