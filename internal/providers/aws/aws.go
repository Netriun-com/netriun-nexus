// SPDX-License-Identifier: AGPL-3.0-only

// Package aws adapts the existing aws compute collector.
package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/internal/compute"
)

// ClientFactory resolves this account's credentials outside the adapter. It
// must return a client scoped to the supplied account and region.
type ClientFactory func(context.Context, providers.Account, string) (*ec2.Client, error)

func New(factory ClientFactory) (providers.Provider, error) {
	if factory == nil {
		return nil, errors.New("aws client factory is required")
	}
	return compute.New("aws", "EC2 instance", true, func(ctx context.Context, account providers.Account, region string) ([]cloud.Instance, error) {
		client, err := factory(ctx, account, region)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, errors.New("aws client factory returned nil")
		}
		return cloud.Inventory(ctx, client)
	}), nil
}
