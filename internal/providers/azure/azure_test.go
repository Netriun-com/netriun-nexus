// SPDX-License-Identifier: AGPL-3.0-only

package azure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/azure"
	"github.com/netriun/nexus/internal/providers/internal/testutil"
)

func TestRecordedInventoryParity(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "azure", Region: ""}
	replay := &testutil.Replay{T: t, Provider: "azure"}
	testutil.DefaultTransport(t, replay)
	client, err := cloud.NewAzureClient(ctx, "tenant", "fixture", "fixture", "sub")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := azure.New(func(ctx context.Context, got providers.Account) (*cloud.AzureClient, error) {
		if got != account {
			t.Fatalf("wrong client scope: %+v", got)
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Check(t, replay, adapter, account, "Azure virtual machine", func() ([]cloud.Instance, error) { return cloud.AzureInventory(ctx, client) })
}

func TestFactoryFailures(t *testing.T) {
	if _, err := azure.New(nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "azure", Region: ""}
	failure := errors.New("factory unavailable")
	for _, expected := range []error{failure, nil} {
		adapter, err := azure.New(func(context.Context, providers.Account) (*cloud.AzureClient, error) { return nil, expected })
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
