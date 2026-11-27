// SPDX-License-Identifier: AGPL-3.0-only
package aws_test

import (
	"context"
	"errors"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providerstest"
	"testing"
)

func TestNetworkRecordedResponses(t *testing.T) {
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "aws", Region: "us-east-1"}
	t.Run("complete", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "aws")
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err != nil || len(items) != 6 {
			t.Fatalf("items=%d err=%v", len(items), err)
		}
		kinds := map[string]int{}
		relations := map[string]bool{}
		for _, item := range items {
			if item.AccountID != 42 || item.WorkspaceID != 17 || item.Provider != "aws" || item.Service != "network" || item.Region != account.Region || item.Summary["provider_type"] == "" || item.SeenAt.IsZero() {
				t.Fatalf("normalization: %+v", item)
			}
			kinds[item.Kind]++
		}
		if kinds["network"] != 2 || kinds["subnet"] != 2 || kinds["load_balancer"] != 2 {
			t.Fatalf("kinds: %v", kinds)
		}
		for _, edge := range edges {
			relations[edge.Relation] = true
			if edge.From.ProviderID == "10.0.0.1" || edge.From.ProviderID == "eni-one" {
				t.Fatal("non-instance backend treated as instance")
			}
		}
		for _, name := range []string{"in_network", "in_subnet", "uses_rule", "behind_lb"} {
			if !relations[name] {
				t.Fatalf("missing relation %s", name)
			}
		}
		if replay.Calls["DescribeVpcs"] != 2 {
			t.Fatal("VPC pagination not followed")
		}
	})
	for _, action := range []string{"DescribeVpcs", "DescribeSubnets", "DescribeLoadBalancers", "DescribeTargetGroups", "DescribeTags", "DescribeTargetHealth", "DescribeInstances"} {
		t.Run(action+"_error", func(t *testing.T) {
			adapter, replay := providerstest.Adapter(t, "aws")
			replay.FailAction = action
			replay.FailPage = 1
			switch action {
			case "DescribeVpcs", "DescribeSubnets", "DescribeLoadBalancers", "DescribeTargetGroups", "DescribeInstances":
				replay.FailPage = 2
			}
			items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
			if err == nil || items != nil || edges != nil {
				t.Fatalf("partial result: %v %v %v", items, edges, err)
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "aws")
		replay.Empty = true
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err != nil || len(items) != 0 || len(edges) != 0 {
			t.Fatalf("empty collection: %v %v %v", items, edges, err)
		}
	})
	t.Run("repeated-token", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "aws")
		replay.RepeatAction = "DescribeVpcs"
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err == nil || items != nil || edges != nil {
			t.Fatal("cyclic pagination accepted")
		}
	})

	t.Run("cancelled-page", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "aws")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		replay.Cancel = cancel
		replay.CancelAction = "DescribeVpcs"
		items, edges, err := adapter.List(ctx, account, "network", account.Region)
		if err == nil || items != nil || edges != nil {
			t.Fatal("cancelled collection returned partial data")
		}
	})
	t.Run("unsupported-region", func(t *testing.T) {
		adapter, _ := providerstest.Adapter(t, "aws")
		if _, _, err := adapter.List(t.Context(), account, "network", "*"); err == nil {
			t.Fatal("global network scope accepted")
		}
	})
	t.Run("get", func(t *testing.T) {
		adapter, _ := providerstest.Adapter(t, "aws")
		item, err := adapter.Get(t.Context(), account, "subnet", "subnet-1")
		if err != nil || item.ProviderID != "subnet-1" {
			t.Fatalf("Get: %+v %v", item, err)
		}
		_, err = adapter.Get(t.Context(), account, "network", "missing")
		if !errors.Is(err, providers.ErrNotFound) {
			t.Fatal(err)
		}
	})
}
