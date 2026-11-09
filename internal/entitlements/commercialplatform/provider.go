// SPDX-License-Identifier: AGPL-3.0-only

// Package commercialplatform provides Community entitlements until the Netriun
// Commercial Platform API is defined. This provider will call that platform once
// its API is defined; if the platform is unavailable, Nexus behaves as Community
// and no data becomes unreadable.
package commercialplatform

import (
	"context"

	"github.com/netriun/nexus/internal/entitlements"
)

// Provider currently delegates all entitlement decisions to CommunityProvider.
type Provider struct {
	Mode entitlements.DeploymentMode
}

var _ entitlements.Provider = Provider{}

// Snapshot returns the Community snapshot for the configured deployment mode.
func (p Provider) Snapshot(ctx context.Context, workspaceID int64) entitlements.Snapshot {
	// TODO: Integrate the Netriun Commercial Platform API once it is defined,
	// preserving the Community fallback when the platform is unavailable.
	return (entitlements.CommunityProvider{Mode: p.Mode}).Snapshot(ctx, workspaceID)
}
