// SPDX-License-Identifier: AGPL-3.0-only

// Package providers defines the provider-facing resource contract independently
// of persistence. Adapters are constructed explicitly and resolved by Registry.
package providers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"
)

const (
	Compute  = "compute"
	Instance = "instance"
)

var (
	ErrUnsupported = errors.New("unsupported provider operation")
	ErrNotFound    = errors.New("provider resource not found")
)

// Account carries identity, not credentials. The client's factory resolves
// credentials for this account. Region selects the scope of Get for regional
// providers; List always uses its explicit region argument.
type Account struct {
	WorkspaceID int64
	ID          int64
	Provider    string
	Region      string
}

// Regional services require a concrete region. Non-regional services enumerate
// the subscription/project and optionally filter the result by region.
type ServiceSpec struct {
	Name            string
	Kinds           []string
	Regional        bool
	DefaultInterval time.Duration
}

// Resource mirrors the T17 columns without depending on a store. Adapters set
// SeenAt; persistence assigns ID, Version, CreatedAt and UpdatedAt.
type Resource struct {
	ID          int64             `json:"id"`
	WorkspaceID int64             `json:"workspace_id"`
	AccountID   int64             `json:"account_id"`
	Provider    string            `json:"provider"`
	Service     string            `json:"service"`
	Kind        string            `json:"kind"`
	Region      string            `json:"region"`
	ProviderID  string            `json:"provider_id"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	Tags        map[string]string `json:"tags"`
	Summary     map[string]any    `json:"summary"`
	Details     map[string]any    `json:"details"`
	SeenAt      time.Time         `json:"seen_at"`
	Version     int64             `json:"version"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Reference identifies a resource within the collection's account scope using
// provider identity. Persistence resolves it to a database ID during reconcile.
type Reference struct {
	Service    string `json:"service"`
	Region     string `json:"region"`
	Kind       string `json:"kind"`
	ProviderID string `json:"provider_id"`
}

// Relation connects provider references within the collection's account scope;
// adapters do not need database IDs to describe either end.
type Relation struct {
	From     Reference `json:"from"`
	To       Reference `json:"to"`
	Relation string    `json:"relation"`
}

type Provider interface {
	Services() []ServiceSpec
	List(context.Context, Account, string, string) ([]Resource, []Relation, error)
	Get(context.Context, Account, string, string) (Resource, error)
}

// Registry is immutable after construction and safe for concurrent lookups.
// Composition belongs to the caller, so this package never imports adapters.
type Registry struct{ adapters map[string]Provider }

func NewRegistry(adapters map[string]Provider) (*Registry, error) {
	for name, adapter := range adapters {
		if name == "" || adapter == nil {
			return nil, errors.New("provider registry requires a name and adapter")
		}
	}
	return &Registry{adapters: maps.Clone(adapters)}, nil
}

func (r *Registry) Lookup(name string) (Provider, error) {
	if adapter, ok := r.adapters[name]; ok {
		return adapter, nil
	}
	return nil, fmt.Errorf("%w: provider %q", ErrUnsupported, name)
}
