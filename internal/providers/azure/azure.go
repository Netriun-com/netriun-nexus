// SPDX-License-Identifier: AGPL-3.0-only

// Package azure adapts the existing azure compute collector.
package azure

import (
	"context"
	"errors"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/internal/compute"
)

// ClientFactory resolves this account's credentials outside the adapter. It
// must return a client scoped to the supplied account.
type ClientFactory func(context.Context, providers.Account) (*cloud.AzureClient, error)

func New(factory ClientFactory) (providers.Provider, error) {
	if factory == nil {
		return nil, errors.New("azure client factory is required")
	}
	return compute.New("azure", "Azure virtual machine", false, func(ctx context.Context, account providers.Account, region string) ([]cloud.Instance, error) {
		client, err := factory(ctx, account)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, errors.New("azure client factory returned nil")
		}
		return cloud.AzureInventory(ctx, client)
	}), nil
}
