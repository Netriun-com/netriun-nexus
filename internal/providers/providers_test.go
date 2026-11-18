// SPDX-License-Identifier: AGPL-3.0-only

package providers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/netriun/nexus/internal/providers"
)

type stub struct{}

func (*stub) Services() []providers.ServiceSpec { return nil }
func (*stub) List(context.Context, providers.Account, string, string) ([]providers.Resource, []providers.Relation, error) {
	return nil, nil, nil
}
func (*stub) Get(context.Context, providers.Account, string, string) (providers.Resource, error) {
	return providers.Resource{}, nil
}

func TestRegistry(t *testing.T) {
	adapter := &stub{}
	entries := map[string]providers.Provider{"aws": adapter, "alibaba": adapter, "azure": adapter, "gcp": adapter}
	registry, err := providers.NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	delete(entries, "aws")
	for _, name := range []string{"aws", "alibaba", "azure", "gcp"} {
		got, err := registry.Lookup(name)
		if err != nil || got != adapter {
			t.Fatalf("%s: %v %v", name, got, err)
		}
	}
	if _, err := registry.Lookup("unknown"); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("unknown: %v", err)
	}
	for _, invalid := range []map[string]providers.Provider{{"": adapter}, {"aws": nil}} {
		if _, err := providers.NewRegistry(invalid); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
}
