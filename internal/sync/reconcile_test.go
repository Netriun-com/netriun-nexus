// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	syncer "github.com/netriun/nexus/internal/sync"
)

func changeAccount(t *testing.T, s *syncer.Store, id int64, provider string, regions []string) {
	t.Helper()
	tx, err := s.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), "UPDATE cloud_accounts SET provider=$2,regions=$3 WHERE id=$1", id, provider, regions); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileAccountTx(t.Context(), tx, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func scopes(t *testing.T, s *syncer.Store, id int64) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	rows, err := s.DB.Query(t.Context(), "SELECT service,region,id FROM sync_state WHERE account_id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var service, region string
		var id int64
		if err := rows.Scan(&service, &region, &id); err != nil {
			t.Fatal(err)
		}
		out[service+":"+region] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAccountScopeReconciliation(t *testing.T) {
	s, _, account := setup(t)
	changeAccount(t, s, account, "aws", []string{"us-east-1"})
	if got := scopes(t, s, account); len(got) != 1 || got["compute:us-east-1"] == 0 {
		t.Fatalf("add: %v", got)
	}
	old := queued(t, s)
	changeAccount(t, s, account, "aws", []string{"us-west-2"})
	if got := scopes(t, s, account); len(got) != 1 || got["compute:us-west-2"] == 0 {
		t.Fatalf("region change: %v", got)
	}
	if err := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) {
		t.Error("obsolete scope collected")
		return false, nil
	})(t.Context(), *old); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue.Complete(t.Context(), *old); err != nil {
		t.Fatal(err)
	}
	changeAccount(t, s, account, "alibaba", []string{"cn-hangzhou", "ap-southeast-1"})
	before := scopes(t, s, account)
	if len(before) != 7 {
		t.Fatalf("Alibaba scopes: %v", before)
	}
	changeAccount(t, s, account, "alibaba", []string{"cn-shanghai"})
	after := scopes(t, s, account)
	if len(after) != 5 || after["compute:cn-shanghai"] == 0 || after["eds.desktops:cn-shanghai"] == 0 {
		t.Fatalf("Alibaba region change: %v", after)
	}
	for _, service := range []string{syncer.Users, syncer.Buckets, syncer.SecurityGroups} {
		key := service + ":*"
		if after[key] == 0 || after[key] != before[key] {
			t.Fatalf("account-wide row changed: %s", key)
		}
	}
	for _, provider := range []string{"azure", "gcp"} {
		changeAccount(t, s, account, provider, []string{"selected-region"})
		if got := scopes(t, s, account); len(got) != 1 || got["compute:*"] == 0 {
			t.Fatalf("%s global scope: %v", provider, got)
		}
	}
	if _, err := s.DB.Exec(t.Context(), "DELETE FROM cloud_accounts WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if got := scopes(t, s, account); len(got) != 0 {
		t.Fatalf("cascade delete: %v", got)
	}
}

func TestUpgradeDiscoveryAndDisabledRows(t *testing.T) {
	s, _, account := setup(t)
	if _, err := s.DB.Exec(t.Context(), "UPDATE cloud_accounts SET regions='{}' WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	discover := func(context.Context, syncer.Account) map[string][]string {
		return map[string][]string{syncer.Compute: {"us-east-1", "eu-west-1"}}
	}
	if err := s.Reconcile(t.Context(), discover); err != nil {
		t.Fatal(err)
	}
	found := scopes(t, s, account)
	if len(found) != 2 {
		t.Fatalf("upgrade: %v", found)
	}
	id := found["compute:us-east-1"]
	if _, err := s.DB.Exec(t.Context(), "UPDATE sync_state SET enabled=false WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(t.Context(), func(context.Context, syncer.Account) map[string][]string { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := scopes(t, s, account); len(got) != 2 || got["compute:us-east-1"] != id {
		t.Fatalf("failed discovery removed known scopes: %v", got)
	}
	if n, err := s.EnqueueDue(t.Context()); err != nil || n != 1 {
		t.Fatalf("disabled scope enqueued: %d %v", n, err)
	}
}

func TestPayloadOwnershipAndRecreatedScope(t *testing.T) {
	s, workspace, account := setup(t)
	id := addState(t, s, account)
	j := queued(t, s)
	handler := s.Handler(func(context.Context, syncer.Payload, syncer.Account) (bool, error) {
		t.Error("invalid scope collected")
		return false, nil
	})
	copied := *j
	copied.Payload, _ = json.Marshal(syncer.Payload{WorkspaceID: workspace + 1, AccountID: account, StateID: id, Service: syncer.Compute, Region: "us-east-1"})
	if err := handler(t.Context(), copied); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(t.Context(), "DELETE FROM sync_state WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	newID := addState(t, s, account)
	if newID == id {
		t.Fatal("state generation reused")
	}
	if err := handler(t.Context(), *j); err != nil {
		t.Fatal(err)
	}
	var untouched bool
	if err := s.DB.QueryRow(t.Context(), "SELECT last_started_at IS NULL AND last_success_at IS NULL FROM sync_state WHERE id=$1", newID).Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	if !untouched {
		t.Fatal("old job changed recreated scope")
	}
}

func TestPartialSnapshotIsFailure(t *testing.T) {
	s, workspace, account := setup(t)
	p := syncer.Payload{WorkspaceID: workspace, AccountID: account, Service: syncer.Buckets, Region: "*"}
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO service_snapshots(workspace_id,account_id,service_key,region,payload) VALUES($1,$2,$3,'',$4::jsonb)`, workspace, account, p.Service, `{"data":[],"failed_buckets":["provider-detail-must-not-be-exposed"]}`); err != nil {
		t.Fatal(err)
	}
	if empty, err := s.Result(t.Context(), p, ""); err == nil || empty || fmt.Sprint(err) != "Collection was incomplete" {
		t.Fatalf("partial: %v %v", empty, err)
	}
}
