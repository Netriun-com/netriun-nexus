// SPDX-License-Identifier: AGPL-3.0-only
package alibaba_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providerstest"
)

func TestNetworkRecordedResponses(t *testing.T) {
	account := providers.Account{WorkspaceID: 17, ID: 42, Provider: "alibaba", Region: "cn-hangzhou"}
	t.Run("complete", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "alibaba")
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err != nil || len(items) != 8 {
			t.Fatalf("items=%d err=%v", len(items), err)
		}
		kinds := map[string]int{}
		relations := map[string]bool{}
		for _, item := range items {
			if item.AccountID != 42 || item.WorkspaceID != 17 || item.Provider != "alibaba" || item.Service != "network" || item.Region != account.Region || item.Summary["provider_type"] == "" || item.SeenAt.IsZero() {
				t.Fatalf("normalization: %+v", item)
			}
			kinds[item.Kind]++
		}
		if kinds["network"] != 2 || kinds["subnet"] != 2 || kinds["load_balancer"] != 4 {
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
	for _, action := range []string{"DescribeVpcs", "DescribeVSwitches", "DescribeLoadBalancers", "ListLoadBalancers", "ListListeners", "ListRules", "ListServerGroupServers", "DescribeInstances", "DescribeLoadBalancerAttribute", "DescribeVServerGroups", "DescribeVServerGroupAttribute", "DescribeMasterSlaveServerGroups", "DescribeMasterSlaveServerGroupAttribute"} {
		t.Run(action+"_error", func(t *testing.T) {
			adapter, replay := providerstest.Adapter(t, "alibaba")
			replay.FailAction = action
			replay.FailPage = 1
			switch action {
			case "DescribeVpcs", "DescribeVSwitches", "DescribeLoadBalancers", "ListLoadBalancers", "ListListeners", "ListRules", "ListServerGroupServers", "DescribeInstances":
				replay.FailPage = 2
			}
			items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
			if err == nil || items != nil || edges != nil {
				t.Fatalf("partial result: %v %v %v", items, edges, err)
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "alibaba")
		replay.Empty = true
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err != nil || len(items) != 0 || len(edges) != 0 {
			t.Fatalf("empty collection: %v %v %v", items, edges, err)
		}
	})
	t.Run("repeated-token", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "alibaba")
		replay.RepeatAction = "ListLoadBalancers"
		items, edges, err := adapter.List(t.Context(), account, "network", account.Region)
		if err == nil || items != nil || edges != nil {
			t.Fatal("cyclic pagination accepted")
		}
	})

	t.Run("cancelled-page", func(t *testing.T) {
		adapter, replay := providerstest.Adapter(t, "alibaba")
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
		adapter, _ := providerstest.Adapter(t, "alibaba")
		if _, _, err := adapter.List(t.Context(), account, "network", "*"); err == nil {
			t.Fatal("global network scope accepted")
		}
	})
	t.Run("get", func(t *testing.T) {
		adapter, _ := providerstest.Adapter(t, "alibaba")
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

func TestUnavailableLoadBalancerKinds(t *testing.T) {
	for _, tc := range []struct {
		action, code string
		page         int
		empty        bool
	}{
		{"DescribeLoadBalancers", "ORDER.OPEND", 1, true},
		{"DescribeLoadBalancers", "OperationFailed.Endpoint", 1, true},
		{"DescribeLoadBalancers", "InvalidProduct.NotFound", 1, true},
		{"ListLoadBalancers", "InvalidProduct.NotFound", 1, true},
		{"DescribeLoadBalancers", "InvalidRegion.NotFound", 1, true},
		{"ListLoadBalancers", "InvalidRegion.NotFound", 1, true},
		{"DescribeVpcs", "InvalidProduct.NotFound", 1, false},
		{"ListLoadBalancers", "InvalidRegion.NotFound", 2, false},
		{"ListLoadBalancers", "UnsupportedRegion", 1, true},
		{"DescribeLoadBalancers", "ORDER.OPEND", 2, false},
		{"ListLoadBalancers", "UnsupportedRegion", 2, false},
		{"DescribeLoadBalancers", "Forbidden", 1, false},
		{"ListLoadBalancers", "Forbidden", 1, false},
		{"ListLoadBalancers", "ServiceUnavailable", 1, false},
		{"DescribeLoadBalancerAttribute", "ORDER.OPEND", 1, false},
		{"DescribeVpcs", "UnsupportedRegion", 1, false},
	} {
		t.Run(tc.action+"/"+tc.code+fmt.Sprint(tc.page), func(t *testing.T) {
			adapter, replay := providerstest.Adapter(t, "alibaba")
			replay.FailAction, replay.FailCode, replay.FailPage = tc.action, tc.code, tc.page
			items, _, err := adapter.List(t.Context(), providers.Account{WorkspaceID: 17, ID: 42, Provider: "alibaba"}, "network", "cn-hangzhou")
			if !tc.empty {
				if err == nil || items != nil {
					t.Fatalf("masked error: %v %v", items, err)
				}
				return
			}
			if err != nil || len(items) != 6 {
				t.Fatalf("unavailable kind lost region: items=%d err=%v", len(items), err)
			}
			for _, item := range items {
				if item.Kind == "load_balancer" && ((tc.action == "DescribeLoadBalancers" && item.Summary["provider_type"] == "CLB/SLB") || (tc.action == "ListLoadBalancers" && item.Summary["provider_type"] == "ALB")) {
					t.Fatalf("unavailable kind survived: %+v", item)
				}
			}
		})
	}
}
