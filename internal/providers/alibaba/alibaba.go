// SPDX-License-Identifier: AGPL-3.0-only

// Package alibaba adapts the existing alibaba compute collector.
package alibaba

import (
	"context"
	"errors"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/internal/compute"
)

// ClientFactory resolves this account's credentials outside the adapter. It
// must return a client scoped to the supplied account and region.
type ClientFactory func(context.Context, providers.Account, string) (*ecs.Client, error)

func New(factory ClientFactory) (providers.Provider, error) {
	if factory == nil {
		return nil, errors.New("alibaba client factory is required")
	}
	return compute.New("alibaba", "ECS instance", true, func(ctx context.Context, account providers.Account, region string) ([]cloud.Instance, error) {
		client, err := factory(ctx, account, region)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, errors.New("alibaba client factory returned nil")
		}
		return cloud.AlibabaInventory(ctx, client, region)
	}), nil
}
