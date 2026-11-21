// SPDX-License-Identifier: AGPL-3.0-only

// Package compute maps the existing compute inventory without introducing
// provider calls or changing the collectors' failure and pagination semantics.
package compute

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
)

type Collect func(context.Context, providers.Account, string) ([]cloud.Instance, error)

type adapter struct {
	name, nativeType string
	regional         bool
	collect          Collect
}

func New(name, nativeType string, regional bool, collect Collect) providers.Provider {
	return &adapter{name: name, nativeType: nativeType, regional: regional, collect: collect}
}

func (a *adapter) Services() []providers.ServiceSpec {
	return []providers.ServiceSpec{{Name: providers.Compute, Kinds: []string{providers.Instance}, Regional: a.regional, DefaultInterval: 5 * time.Minute}}
}

func (a *adapter) List(ctx context.Context, account providers.Account, service, region string) ([]providers.Resource, []providers.Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if service != providers.Compute {
		return nil, nil, fmt.Errorf("%w: service %q", providers.ErrUnsupported, service)
	}
	if account.WorkspaceID <= 0 || account.ID <= 0 || account.Provider != a.name {
		return nil, nil, fmt.Errorf("invalid %s account identity", a.name)
	}
	if a.regional && (region == "" || region == "*") {
		return nil, nil, fmt.Errorf("%s compute requires a concrete region", a.name)
	}
	instances, err := a.collect(ctx, account, region)
	if err != nil {
		return nil, nil, err
	}
	// Never expose a partial inventory after cancellation, even for collectors
	// whose SDK does not propagate context through an in-flight request.
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	seenAt := time.Now().UTC()
	result := make([]providers.Resource, 0, len(instances))
	for _, item := range instances {
		itemRegion := item.Region
		if a.regional {
			itemRegion = region
		}
		if !a.regional && region != "" && region != "*" && itemRegion != region {
			continue
		}
		tags, _ := item.Details["tags"].(map[string]string)
		tags = maps.Clone(tags)
		if tags == nil {
			tags = map[string]string{}
		}
		result = append(result, providers.Resource{
			WorkspaceID: account.WorkspaceID, AccountID: account.ID, Provider: a.name,
			Service: providers.Compute, Kind: providers.Instance, Region: itemRegion,
			ProviderID: item.ID, Name: item.Name, State: item.State, Tags: tags,
			Summary: map[string]any{"provider_type": a.nativeType, "instance_type": item.Type, "public_ip": item.PublicIP, "private_ip": item.PrivateIP},
			Details: maps.Clone(item.Details), SeenAt: seenAt,
		})
	}
	return result, []providers.Relation{}, nil
}

// Get currently returns the inventory representation. It deliberately shares
// List's mapping and completeness checks; richer details are a later task.
func (a *adapter) Get(ctx context.Context, account providers.Account, kind, id string) (providers.Resource, error) {
	if kind != providers.Instance {
		return providers.Resource{}, fmt.Errorf("%w: kind %q", providers.ErrUnsupported, kind)
	}
	if id == "" {
		return providers.Resource{}, fmt.Errorf("instance id is required")
	}
	items, _, err := a.List(ctx, account, providers.Compute, account.Region)
	if err != nil {
		return providers.Resource{}, err
	}
	for _, item := range items {
		if item.ProviderID == id {
			return item, nil
		}
	}
	return providers.Resource{}, fmt.Errorf("%w: %s %q", providers.ErrNotFound, kind, id)
}
