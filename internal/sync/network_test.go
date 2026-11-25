// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/providers"
	syncer "github.com/netriun/nexus/internal/sync"
)

func TestNetworkSchedulingAndLeaseFence(t *testing.T) {
	s, workspace, account := setup(t)
	s.Config.Network = 23 * time.Minute
	changeAccount(t, s, account, "aws", []string{"us-east-1"})
	var seconds float64
	if err := s.DB.QueryRow(t.Context(), `SELECT extract(epoch FROM "interval") FROM sync_state WHERE account_id=$1 AND service='network'`, account).Scan(&seconds); err != nil || seconds != 1380 {
		t.Fatalf("network interval: %v %v", seconds, err)
	}
	run := func(items []providers.Resource, expire bool) error {
		t.Helper()
		if _, err := s.RefreshService(t.Context(), workspace, []int64{account}, syncer.Network, "us-east-1"); err != nil {
			t.Fatal(err)
		}
		job, err := claimReady(t, s, "network", time.Minute, syncer.JobKind)
		if err != nil || job == nil {
			t.Fatalf("claim %v %v", job, err)
		}
		err = s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
			if expire {
				if _, err := s.DB.Exec(ctx, `UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
					return false, err
				}
			}
			return len(items) == 0, s.ReconcileNetwork(ctx, p, items, nil)
		})(t.Context(), *job)
		if !expire {
			if err := s.Queue.Complete(t.Context(), *job); err != nil {
				t.Fatal(err)
			}
		}
		return err
	}
	item := providers.Resource{WorkspaceID: workspace, AccountID: account, Provider: "aws", Service: "network", Kind: "network", Region: "us-east-1", ProviderID: "vpc-old", Tags: map[string]string{}, Summary: map[string]any{}, Details: map[string]any{}}
	if err := run([]providers.Resource{item}, false); err != nil {
		t.Fatal(err)
	}
	item.ProviderID = "vpc-stale"
	if err := run([]providers.Resource{item}, true); err == nil {
		t.Fatal("stale lease accepted")
	}
	var id string
	if err := s.DB.QueryRow(t.Context(), `SELECT provider_id FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service='network'`, workspace, account).Scan(&id); err != nil || id != "vpc-old" {
		t.Fatalf("stale network write: %s %v", id, err)
	}
}

func TestNetworkFinalLeaseAndTransactionRollback(t *testing.T) {
	s, workspace, account := setup(t)
	changeAccount(t, s, account, "aws", []string{"us-east-1"})
	if _, err := s.DB.Exec(t.Context(), `CREATE FUNCTION expire_network_lease() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE status='running'; RETURN NEW; END $$; CREATE TRIGGER expire_network_lease BEFORE INSERT ON resources FOR EACH ROW EXECUTE FUNCTION expire_network_lease()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshService(t.Context(), workspace, []int64{account}, syncer.Network, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	job, err := claimReady(t, s, "network", time.Minute, syncer.JobKind)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	err = s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
		var items []providers.Resource
		for _, kind := range []string{"network", "subnet", "load_balancer"} {
			items = append(items, providers.Resource{WorkspaceID: workspace, AccountID: account, Provider: "aws", Service: "network", Kind: kind, Region: "us-east-1", ProviderID: kind, Tags: map[string]string{}, Summary: map[string]any{}, Details: map[string]any{}})
		}
		return false, s.ReconcileNetwork(ctx, p, items, nil)
	})(t.Context(), *job)
	if err == nil {
		t.Fatal("lost lease committed")
	}
	var count int
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM resources WHERE account_id=$1`, account).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial transaction: %d %v", count, err)
	}
}
