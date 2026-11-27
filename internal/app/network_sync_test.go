// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providerstest"
	"github.com/netriun/nexus/internal/resources"
	syncer "github.com/netriun/nexus/internal/sync"
)

func TestNetworkFullSyncRelations(t *testing.T) {
	for _, provider := range []string{"aws", "alibaba"} {
		t.Run(provider, func(t *testing.T) {
			region := "us-east-1"
			if provider == "alibaba" {
				region = "cn-hangzhou"
			}
			a, workspace, account := computeTestApp(t, provider, region)
			adapter, replay := providerstest.Adapter(t, provider)
			registry, err := providers.NewRegistry(map[string]providers.Provider{provider: adapter})
			if err != nil {
				t.Fatal(err)
			}
			a.Providers = registry
			if err := runComputeTest(t, a, t.Context(), workspace, account, region); err != nil {
				t.Fatal(err)
			}
			tx, err := a.DB.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := resources.Reconcile(t.Context(), tx, resources.Scope{WorkspaceID: workspace, AccountID: account, Provider: provider, Service: syncer.SecurityGroups, Region: region, Kind: "security_group"}, []resources.Item{{ProviderID: "sg-1"}, {ProviderID: "sg-2"}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			legacy := func() string {
				t.Helper()
				var raw string
				if err := a.DB.QueryRow(t.Context(), `SELECT jsonb_build_object('instances',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM instances i WHERE account_id=$1),'snapshots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM service_snapshots s WHERE account_id=$1))::text`, account).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			beforeLegacy := legacy()
			run := func(ctx context.Context) error {
				t.Helper()
				if _, err := a.Sync.RefreshService(t.Context(), workspace, []int64{account}, syncer.Network, region); err != nil {
					t.Fatal(err)
				}
				job, err := a.Sync.Queue.Claim(t.Context(), "network", time.Minute, syncer.JobKind)
				if err != nil || job == nil {
					t.Fatalf("claim: %v %v", job, err)
				}
				err = a.Sync.Handler(a.CollectScheduled)(ctx, *job)
				if err := a.Sync.Queue.Complete(t.Context(), *job); err != nil {
					t.Fatal(err)
				}
				return err
			}
			if err := run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if legacy() != beforeLegacy {
				t.Fatal("network sync wrote legacy tables")
			}
			edges := func() []string {
				t.Helper()
				rows, err := a.DB.Query(t.Context(), `SELECT src.provider_id||' '||rr.relation||' '||dst.provider_id FROM resource_relations rr JOIN resources src ON src.id=rr.from_id JOIN resources dst ON dst.id=rr.to_id WHERE src.workspace_id=$1 AND src.account_id=$2 AND src.region=$3 AND rr.owner_service='network' ORDER BY 1`, workspace, account, region)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				out := []string{}
				for rows.Next() {
					var edge string
					if err := rows.Scan(&edge); err != nil {
						t.Fatal(err)
					}
					out = append(out, edge)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return out
			}
			expected := []string{"subnet-1 in_network vpc-1", "subnet-2 in_network vpc-2", "i-1 in_subnet subnet-1", "i-2 in_subnet subnet-2", "i-1 uses_rule sg-1", "i-2 uses_rule sg-2"}
			if provider == "aws" {
				for i := 1; i <= 2; i++ {
					id := fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/lb-%d/%d", i, i)
					expected = append(expected, fmt.Sprintf("%s in_network vpc-%d", id, i), "i-1 behind_lb "+id)
				}
			} else {
				for i := 1; i <= 2; i++ {
					expected = append(expected, fmt.Sprintf("lb-%d in_network vpc-1", i), fmt.Sprintf("alb-%d in_network vpc-%d", i, i), fmt.Sprintf("i-1 behind_lb lb-%d", i), fmt.Sprintf("i-2 behind_lb lb-%d", i), fmt.Sprintf("i-1 behind_lb alb-%d", i))
				}
			}
			slices.Sort(expected)
			if got := edges(); !reflect.DeepEqual(got, expected) {
				t.Fatalf("edges: got %v want %v", got, expected)
			}
			// Compute reconciliation must not erase network-owned instance relations.
			if err := runComputeTest(t, a, t.Context(), workspace, account, region); err != nil {
				t.Fatal(err)
			}
			if got := edges(); !reflect.DeepEqual(got, expected) {
				t.Fatalf("compute erased network edges: %v", got)
			}
			snapshot := func() string {
				t.Helper()
				var raw string
				if err := a.DB.QueryRow(t.Context(), `SELECT jsonb_build_object('items',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM resources r WHERE account_id=$1),'edges',(SELECT jsonb_agg(to_jsonb(rr) ORDER BY rr.from_id,rr.to_id,rr.relation) FROM resource_relations rr JOIN resources r ON r.id=rr.from_id WHERE r.account_id=$1))::text`, account).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := snapshot()
			replay.FailAction = "DescribeSubnets"
			if provider == "alibaba" {
				replay.FailAction = "DescribeVSwitches"
			}
			replay.FailPage = 2
			if err := run(t.Context()); err == nil {
				t.Fatal("failed region succeeded")
			}
			if snapshot() != before {
				t.Fatal("failed region changed rows or relations")
			}
			replay.FailAction = ""
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			replay.Cancel = cancel
			replay.CancelAction = "DescribeVpcs"
			if err := run(ctx); err == nil {
				t.Fatal("cancelled region succeeded")
			}
			if snapshot() != before {
				t.Fatal("cancelled region changed rows or relations")
			}
			replay.CancelAction = ""
			replay.Empty = true
			if err := run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := edges(); len(got) != 0 {
				t.Fatalf("empty sync kept owned relations: %v", got)
			}
			var networkCount, otherCount int
			if err := a.DB.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE service='network'),count(*) FILTER(WHERE service<>'network') FROM resources WHERE account_id=$1`, account).Scan(&networkCount, &otherCount); err != nil || networkCount != 0 || otherCount != 4 {
				t.Fatalf("empty sync escaped scope: %d %d %v", networkCount, otherCount, err)
			}
		})
	}
}

// The worker must compose T20's service adapter with T22's network wrapper.
// Constructing the registry advertises all services without resolving credentials.
func TestWorkerProviderRegistryServicesAndNetwork(t *testing.T) {
	a := &App{}
	registry, err := a.newProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for provider, expected := range map[string][]string{
		"aws":     {"compute", "network"},
		"alibaba": {"compute", "eds.desktops", "eds.users", "oss.buckets", "ecs.security_groups", "network"},
		"azure":   {"compute"},
		"gcp":     {"compute"},
	} {
		adapter, err := registry.Lookup(provider)
		if err != nil {
			t.Fatal(err)
		}
		actual := []string{}
		for _, spec := range adapter.Services() {
			actual = append(actual, spec.Name)
		}
		slices.Sort(actual)
		slices.Sort(expected)
		if !slices.Equal(actual, expected) {
			t.Fatalf("%s services: got %v, want %v", provider, actual, expected)
		}
	}
}
