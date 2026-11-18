// SPDX-License-Identifier: AGPL-3.0-only

package gcp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/gcp"
	"github.com/netriun/nexus/internal/providers/internal/testutil"
)

func TestRecordedInventoryParity(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "gcp", Region: ""}
	replay := &testutil.Replay{T: t, Provider: "gcp"}
	testutil.DefaultTransport(t, replay)
	client, err := cloud.NewGCPClient(ctx, "project", testutil.GCPKey(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := gcp.New(func(ctx context.Context, got providers.Account) (*cloud.GCPClient, error) {
		if got != account {
			t.Fatalf("wrong client scope: %+v", got)
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Check(t, replay, adapter, account, "Compute Engine instance", func() ([]cloud.Instance, error) { return cloud.GCPInventory(ctx, client) })
}

func TestFactoryFailures(t *testing.T) {
	if _, err := gcp.New(nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "gcp", Region: ""}
	failure := errors.New("factory unavailable")
	for _, expected := range []error{failure, nil} {
		adapter, err := gcp.New(func(context.Context, providers.Account) (*cloud.GCPClient, error) { return nil, expected })
		if err != nil {
			t.Fatal(err)
		}
		items, relations, err := adapter.List(context.Background(), account, providers.Compute, account.Region)
		if err == nil || items != nil || relations != nil {
			t.Fatalf("factory failure: %v %v %v", items, relations, err)
		}
		if expected != nil && !errors.Is(err, expected) {
			t.Fatalf("factory error lost: %v", err)
		}
	}
}
