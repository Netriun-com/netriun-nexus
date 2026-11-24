// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"github.com/netriun/nexus/internal/providers"
	syncer "github.com/netriun/nexus/internal/sync"
	"testing"
	"time"
)

func computeResource(workspace, account int64, provider, region, id string) providers.Resource {
	return providers.Resource{WorkspaceID: workspace, AccountID: account, Provider: provider, Service: "compute", Kind: "instance", Region: region, ProviderID: id, Name: id, State: "running", Tags: map[string]string{}, Summary: map[string]any{"instance_type": "small", "public_ip": "", "private_ip": ""}, Details: map[string]any{}}
}

// Force failures after the instances write to prove the catalog participates in
// the very same transaction, including the final lease check before commit.
func TestInventoryDualWriteRollback(t *testing.T) {
	for _, mode := range []string{"catalog-error", "lease-expired-before-commit", "cancelled", "invalid-scope"} {
		t.Run(mode, func(t *testing.T) {
			s, workspace, account := setup(t)
			addState(t, s, account)
			run := func(items []providers.Resource, cancelled bool) error {
				t.Helper()
				if _, err := s.RefreshCompute(t.Context(), workspace, []int64{account}, "us-east-1"); err != nil {
					t.Fatal(err)
				}
				job, err := claimReady(t, s, "dual-write", time.Minute, syncer.JobKind)
				if err != nil || job == nil {
					t.Fatalf("claim: %v %v", job, err)
				}
				err = s.Handler(func(ctx context.Context, p syncer.Payload, _ syncer.Account) (bool, error) {
					if cancelled {
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					return false, s.ReconcileInventory(ctx, account, items, nil, false, p.Region)
				})(t.Context(), *job)
				if e := s.Queue.Complete(t.Context(), *job); e != nil {
					t.Fatal(e)
				}
				return err
			}
			if err := run([]providers.Resource{computeResource(workspace, account, "aws", "us-east-1", "original")}, false); err != nil {
				t.Fatal(err)
			}
			snapshot := func() string {
				t.Helper()
				var raw string
				err := s.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
     'instances',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM instances i WHERE account_id=$1),
     'resources',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM resources r WHERE account_id=$1))::text`, account).Scan(&raw)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := snapshot()
			if mode == "catalog-error" || mode == "lease-expired-before-commit" {
				action := `RAISE EXCEPTION 'test catalog write failure';`
				if mode == "lease-expired-before-commit" {
					action = `UPDATE jobs SET locked_until=clock_timestamp()-interval '1 second' WHERE status='running';`
				}
				_, err := s.DB.Exec(t.Context(), `CREATE FUNCTION fail_catalog() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+action+` RETURN NEW; END $$;
     CREATE TRIGGER fail_catalog BEFORE INSERT ON resources FOR EACH ROW EXECUTE FUNCTION fail_catalog()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			updated := computeResource(workspace, account, "aws", "us-east-1", "changed")
			if mode == "invalid-scope" {
				updated.WorkspaceID++
			}
			if err := run([]providers.Resource{updated}, mode == "cancelled"); err == nil {
				t.Fatal("invalid sync succeeded")
			}
			if after := snapshot(); after != before {
				t.Fatalf("%s committed part of inventory", mode)
			}
		})
	}
}
