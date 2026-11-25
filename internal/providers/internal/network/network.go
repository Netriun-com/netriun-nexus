// SPDX-License-Identifier: AGPL-3.0-only

// Package network supplies common normalization and service dispatch for the
// read-only regional network adapters. It has no persistence dependencies.
package network

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
)

const Service = "network"

var Kinds = []string{"network", "subnet", "load_balancer"}

type Collect func(context.Context, providers.Account, string) ([]providers.Resource, []providers.Relation, error)
type adapter struct {
	base     providers.Provider
	provider string
	collect  Collect
}

func New(base providers.Provider, provider string, collect Collect) (providers.Provider, error) {
	if base == nil || collect == nil {
		return nil, errors.New("network adapter requires a base and collector")
	}
	return &adapter{base: base, provider: provider, collect: collect}, nil
}
func (a *adapter) Services() []providers.ServiceSpec {
	return append(append([]providers.ServiceSpec(nil), a.base.Services()...), providers.ServiceSpec{Name: Service, Kinds: append([]string(nil), Kinds...), Regional: true, DefaultInterval: 15 * time.Minute})
}
func (a *adapter) List(ctx context.Context, account providers.Account, service, region string) ([]providers.Resource, []providers.Relation, error) {
	if service != Service {
		return a.base.List(ctx, account, service, region)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if account.WorkspaceID <= 0 || account.ID <= 0 || account.Provider != a.provider || region == "" || region == "*" {
		return nil, nil, errors.New("network sync requires an account and concrete region")
	}
	items, edges, err := a.collect(ctx, account, region)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, nil, err
	}
	seen := time.Now().UTC()
	for i := range items {
		item := &items[i]
		if item.ProviderID == "" {
			return nil, nil, errors.New("network response missing resource identity")
		}
		item.WorkspaceID = account.WorkspaceID
		item.AccountID = account.ID
		item.Provider = account.Provider
		item.Region = region
		item.Service = Service
		item.SeenAt = seen
	}
	return items, Unique(edges), nil
}
func (a *adapter) Get(ctx context.Context, account providers.Account, kind, id string) (providers.Resource, error) {
	switch kind {
	case "network", "subnet", "load_balancer":
	default:
		return a.base.Get(ctx, account, kind, id)
	}
	items, _, err := a.List(ctx, account, Service, account.Region)
	if err != nil {
		return providers.Resource{}, err
	}
	for _, item := range items {
		if item.Kind == kind && item.ProviderID == id {
			return item, nil
		}
	}
	return providers.Resource{}, fmt.Errorf("%w: %s %s", providers.ErrNotFound, kind, id)
}
func Item(kind, id, name, state, native string, tags map[string]string, details map[string]any) providers.Resource {
	if tags == nil {
		tags = map[string]string{}
	}
	if name == "" {
		name = id
	}
	return providers.Resource{Kind: kind, ProviderID: id, Name: name, State: state, Tags: tags, Summary: map[string]any{"provider_type": native}, Details: details}
}
func Ref(service, kind, region, id string) providers.Reference {
	return providers.Reference{Service: service, Kind: kind, Region: region, ProviderID: id}
}
func Edge(from, to providers.Reference, relation string) providers.Relation {
	return providers.Relation{From: from, To: to, Relation: relation}
}
func Unique(edges []providers.Relation) []providers.Relation {
	seen := map[providers.Relation]bool{}
	out := make([]providers.Relation, 0, len(edges))
	for _, edge := range edges {
		if edge.From.ProviderID != "" && edge.To.ProviderID != "" && !seen[edge] {
			seen[edge] = true
			out = append(out, edge)
		}
	}
	return out
}

// InstanceEdges uses the same compute identities and details as existing sync.
func InstanceEdges(items []cloud.Instance, region string) []providers.Relation {
	var edges []providers.Relation
	for _, item := range items {
		from := Ref("compute", "instance", region, item.ID)
		subnet, _ := item.Details["subnet_id"].(string)
		if subnet != "" {
			edges = append(edges, Edge(from, Ref(Service, "subnet", region, subnet), "in_subnet"))
		}
		groups, _ := item.Details["security_group_ids"].([]string)
		for _, group := range groups {
			edges = append(edges, Edge(from, Ref("ecs.security_groups", "security_group", region, group), "uses_rule"))
		}
	}
	return edges
}

// CheckToken rejects cyclic pagination rather than accepting a partial result.
func CheckToken(seen map[string]bool, token string) error {
	if token == "" {
		return nil
	}
	if seen[token] {
		return errors.New("provider repeated a pagination token")
	}
	seen[token] = true
	return nil
}
