// SPDX-License-Identifier: AGPL-3.0-only

package alibaba_test

import (
	"context"
	"errors"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
	"github.com/netriun/nexus/internal/providers/internal/testutil"
)

func TestRecordedInventoryParity(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "alibaba", Region: "cn-hangzhou"}
	replay := &testutil.Replay{T: t, Provider: "alibaba"}
	client, err := cloud.AlibabaClient(account.Region, "fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	client.HttpClient = replay
	adapter, err := alibaba.New(func(ctx context.Context, got providers.Account, region string) (*ecs.Client, error) {
		if got != account || region != account.Region {
			t.Fatalf("wrong client scope: %+v", got)
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Check(t, replay, adapter, account, "ECS instance", func() ([]cloud.Instance, error) { return cloud.AlibabaInventory(ctx, client, account.Region) })
}

func TestFactoryFailures(t *testing.T) {
	if _, err := alibaba.New(nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "alibaba", Region: "cn-hangzhou"}
	failure := errors.New("factory unavailable")
	for _, expected := range []error{failure, nil} {
		adapter, err := alibaba.New(func(context.Context, providers.Account, string) (*ecs.Client, error) { return nil, expected })
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
