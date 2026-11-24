// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"testing"
)

func TestReconcileAllRegionsScopeAndEmpty(t *testing.T) {
	s, scope := setup(t)
	gone := scope
	gone.Region = "disappeared"
	otherAccount := newScope(t, s, scope.WorkspaceID)
	otherWorkspace := newScope(t, s, 0)
	otherService := scope
	otherService.Service = "network"
	otherKind := scope
	otherKind.Kind = "volume"
	for _, seed := range []Scope{scope, gone, otherAccount, otherWorkspace, otherService, otherKind} {
		reconcile(t, s, seed, []Item{item("old")})
	}
	all := scope
	all.Region = "*"
	run := func(items map[string][]Item) {
		t.Helper()
		tx, err := s.db.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err := ReconcileAllRegions(t.Context(), tx, all, items, nil); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	run(map[string][]Item{scope.Region: {item("new")}, "new-region": {item("new")}})
	if got := listing(t, s, scope); len(got) != 1 || got[0].ProviderID != "new" {
		t.Fatalf("upsert: %+v", got)
	}
	if got := listing(t, s, gone); len(got) != 0 {
		t.Fatalf("disappeared region not cleared: %+v", got)
	}
	run(nil)
	var count int
	if err := s.db.QueryRow(t.Context(), `SELECT count(*) FROM resources WHERE workspace_id=$1 AND account_id=$2 AND service=$3 AND kind=$4`, scope.WorkspaceID, scope.AccountID, scope.Service, scope.Kind).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty collection: count=%d err=%v", count, err)
	}
	for _, kept := range []Scope{otherAccount, otherWorkspace, otherService, otherKind} {
		if got := listing(t, s, kept); len(got) != 1 || got[0].ProviderID != "old" {
			t.Fatalf("unrelated scope changed: %+v %+v", kept, got)
		}
	}
}
