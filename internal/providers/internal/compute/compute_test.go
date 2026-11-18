// SPDX-License-Identifier: AGPL-3.0-only

package compute_test

import (
	"context"
	"errors"
	"testing"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/internal/compute"
)

func TestCancellationAfterCollectionDiscardsResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	adapter := compute.New("aws", "EC2 instance", true, func(context.Context, providers.Account, string) ([]cloud.Instance, error) {
		cancel()
		return []cloud.Instance{{ID: "i-one"}}, nil
	})
	account := providers.Account{WorkspaceID: 1, ID: 2, Provider: "aws", Region: "us-east-1"}
	items, relations, err := adapter.List(ctx, account, providers.Compute, account.Region)
	if !errors.Is(err, context.Canceled) || items != nil || relations != nil {
		t.Fatalf("cancelled inventory: %v %v %v", items, relations, err)
	}
}

func TestRegionalListUsesExplicitScope(t *testing.T) {
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "aws", Region: "us-east-1"}
	adapter := compute.New("aws", "EC2 instance", true, func(_ context.Context, got providers.Account, region string) ([]cloud.Instance, error) {
		if got != account || region != "eu-west-1" {
			t.Fatalf("client scope: %+v %s", got, region)
		}
		return []cloud.Instance{{ID: "i-one", Region: "ignored"}}, nil
	})
	items, _, err := adapter.List(context.Background(), account, providers.Compute, "eu-west-1")
	if err != nil || len(items) != 1 || items[0].Region != "eu-west-1" || items[0].WorkspaceID != 17 || items[0].AccountID != 42 {
		t.Fatalf("scope: %+v %v", items, err)
	}
}
