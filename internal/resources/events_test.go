// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type eventRow struct {
	ID, Workspace, Account, Resource, Version int64
	Type, Service, Region                     string
}

func events(t *testing.T, s *Store, scope Scope, after int64) []eventRow {
	t.Helper()
	rows, err := s.db.Query(t.Context(), `SELECT id,workspace_id,account_id,type,service,region,resource_id,version FROM resource_events WHERE workspace_id=$1 AND account_id=$2 AND id>$3 AND type LIKE 'resource.%' ORDER BY id`, scope.WorkspaceID, scope.AccountID, after)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.ID, &e.Workspace, &e.Account, &e.Type, &e.Service, &e.Region, &e.Resource, &e.Version); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func eventHead(t *testing.T, s *Store, scope Scope) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(t.Context(), `SELECT coalesce(max(id),0) FROM resource_events WHERE workspace_id=$1`, scope.WorkspaceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func beginEvents(t *testing.T, s *Store) pgx.Tx {
	t.Helper()
	tx, err := s.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func TestResourceEventsMixedReconcile(t *testing.T) {
	s, scope := setup(t)
	reconcile(t, s, scope, []Item{item("changed"), item("same"), item("removed")})
	head := eventHead(t, s, scope)
	old := listing(t, s, scope)
	changed := item("changed")
	changed.State = "stopped"
	reconcile(t, s, scope, []Item{item("created"), changed, item("same")})
	got := events(t, s, scope, head)
	if len(got) != 3 {
		t.Fatalf("want three resource events: %+v", got)
	}
	expected := map[string]int64{"resource.created": 1, "resource.updated": 2, "resource.deleted": 1}
	for _, e := range got {
		if version, ok := expected[e.Type]; !ok || version != e.Version || e.Workspace != scope.WorkspaceID || e.Account != scope.AccountID || e.Service != scope.Service || e.Region != scope.Region {
			t.Fatalf("event: %+v", e)
		}
		delete(expected, e.Type)
		if e.Type == "resource.deleted" {
			for _, r := range old {
				if r.ProviderID == "removed" && r.ID != e.Resource {
					t.Fatalf("deleted event lost identity: %+v", e)
				}
			}
		}
	}
	if len(expected) != 0 {
		t.Fatal(expected)
	}
	// JSONB equality and seen_at-only updates must remain silent at scale.
	large := make([]Item, 10000)
	for i := range large {
		large[i] = item(fmt.Sprintf("item-%05d", i))
	}
	reconcile(t, s, scope, large)
	head = eventHead(t, s, scope)
	for i := range large {
		large[i].Tags = []byte(`{"b":2,"a":1.0}`)
	}
	reconcile(t, s, scope, large)
	if got := events(t, s, scope, head); len(got) != 0 {
		t.Fatalf("unchanged 10000 emitted %d events", len(got))
	}
}

func TestResourceEventsRollbackAndNotify(t *testing.T) {
	s, scope := setup(t)
	listener, err := pgx.ConnectConfig(t.Context(), s.db.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close(context.Background())
	if _, err := listener.Exec(t.Context(), `LISTEN nexus_events`); err != nil {
		t.Fatal(err)
	}
	tx := beginEvents(t, s)
	pid := tx.Conn().PgConn().PID()
	if err := Reconcile(t.Context(), tx, scope, []Item{item("one")}, nil); err != nil {
		t.Fatal(err)
	}
	second := scope
	second.Region = "us-west-2"
	if err := Reconcile(t.Context(), tx, second, []Item{item("two")}, nil); err != nil {
		t.Fatal(err)
	}
	none := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
		defer cancel()
		for {
			n, err := listener.WaitForNotification(ctx)
			if errors.Is(err, context.DeadlineExceeded) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n.PID == pid {
				t.Fatalf("unexpected notification: %+v", n)
			}
		}
	}
	none()
	if eventHead(t, s, scope) != 0 {
		t.Fatal("uncommitted events visible")
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		n, err := listener.WaitForNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n.PID != pid {
			continue
		}
		want := fmt.Sprintf("%d:%d", scope.WorkspaceID, eventHead(t, s, scope))
		if n.Channel != "nexus_events" || n.Payload != want {
			t.Fatalf("notification=%+v want=%s", n, want)
		}
		break
	}
	none() // exactly one final maximum, despite two reconciles
	head := eventHead(t, s, scope)
	tx = beginEvents(t, s)
	pid = tx.Conn().PgConn().PID()
	if err := Reconcile(t.Context(), tx, scope, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	none()
	if eventHead(t, s, scope) != head {
		t.Fatal("rollback persisted events")
	}
	// A statement failure after events were generated rolls everything back too.
	tx = beginEvents(t, s)
	pid = tx.Conn().PgConn().PID()
	if err := Reconcile(t.Context(), tx, scope, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT 1/0`); err == nil {
		t.Fatal("expected SQL failure")
	}
	if err := tx.Commit(t.Context()); err == nil {
		t.Fatal("aborted transaction committed")
	}
	none()
	if eventHead(t, s, scope) != head {
		t.Fatal("failed transaction persisted events")
	}
}

func TestResourceEventsAllRegionsAndRelations(t *testing.T) {
	s, scope := setup(t)
	reconcile(t, s, scope, []Item{item("source")})
	target := scope
	target.Service = "network"
	target.Kind = "subnet"
	reconcile(t, s, target, []Item{item("target")})
	edge := Relation{From: Reference{Service: scope.Service, Region: scope.Region, Kind: scope.Kind, ProviderID: "source"}, To: Reference{Service: target.Service, Region: target.Region, Kind: target.Kind, ProviderID: "target"}, Relation: "attached_to"}
	head := eventHead(t, s, scope)
	reconcile(t, s, scope, []Item{item("source")}, edge)
	got := events(t, s, scope, head)
	if len(got) != 1 || got[0].Type != "resource.updated" || got[0].Service != scope.Service || got[0].Version != 1 {
		t.Fatalf("edge addition: %+v", got)
	}
	head = eventHead(t, s, scope)
	reconcile(t, s, scope, []Item{item("source")}, edge)
	if got := events(t, s, scope, head); len(got) != 0 {
		t.Fatalf("unchanged edge: %+v", got)
	}
	// A target deletion cascades to the edge and invalidates its surviving source.
	head = eventHead(t, s, scope)
	reconcile(t, s, target, nil)
	got = events(t, s, scope, head)
	if len(got) != 2 {
		t.Fatalf("target deletion: %+v", got)
	}
	// Service-owned cross-service edges use the same comparison. Repeat a full
	// multi-kind network collection, matching the sync writer's transaction.
	reconcile(t, s, target, []Item{item("target")})
	run := func(edges []Relation) {
		t.Helper()
		tx := beginEvents(t, s)
		if err := ReconcileItems(t.Context(), tx, target, []Item{item("target")}); err != nil {
			t.Fatal(err)
		}
		if err := ReconcileServiceRelations(t.Context(), tx, target, edges); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	head = eventHead(t, s, scope)
	run([]Relation{edge})
	if len(events(t, s, scope, head)) != 1 {
		t.Fatal("service edge not emitted")
	}
	head = eventHead(t, s, scope)
	run([]Relation{edge})
	if len(events(t, s, scope, head)) != 0 {
		t.Fatal("unchanged service relation emitted")
	}
	run(nil)
	if got := events(t, s, scope, head); len(got) != 1 || got[0].Service != scope.Service {
		t.Fatalf("service edge removal: %+v", got)
	}
	// A network-owned source must also retain its unchanged outgoing edge while
	// its own kind is reconciled before ReconcileServiceRelations runs.
	root := target
	root.Kind = "network"
	reconcile(t, s, root, []Item{item("vpc")})
	ownedEdge := Relation{From: Reference{Service: target.Service, Region: target.Region, Kind: target.Kind, ProviderID: "target"}, To: Reference{Service: root.Service, Region: root.Region, Kind: root.Kind, ProviderID: "vpc"}, Relation: "belongs_to"}
	run([]Relation{ownedEdge})
	head = eventHead(t, s, scope)
	run([]Relation{ownedEdge})
	if got := events(t, s, scope, head); len(got) != 0 {
		t.Fatalf("unchanged network-owned source emitted: %+v", got)
	}
	// Account-wide replacement includes deleted regions and real item regions.
	all := scope
	all.Service = "oss.buckets"
	all.Kind = "bucket"
	all.Region = "*"
	runAll := func(items map[string][]Item) {
		t.Helper()
		tx := beginEvents(t, s)
		if err := ReconcileAllRegions(t.Context(), tx, all, items, nil); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	runAll(map[string][]Item{"east": {item("east")}, "west": {item("west")}})
	head = eventHead(t, s, scope)
	runAll(map[string][]Item{"east": {item("east")}, "north": {item("north")}})
	got = events(t, s, scope, head)
	if len(got) != 2 {
		t.Fatalf("all regions: %+v", got)
	}
	for _, e := range got {
		if e.Region == "*" || e.Service != all.Service {
			t.Fatalf("invalid scope %+v", e)
		}
	}
	// Account deletion cascades events as well as resources, without FK failures.
	if _, err := s.db.Exec(t.Context(), `DELETE FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, scope.AccountID, scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if len(events(t, s, scope, 0)) != 0 {
		t.Fatal("events survived account deletion")
	}
}

func TestResourceEventCommitOrder(t *testing.T) {
	s, scope := setup(t)
	other := newScope(t, s, scope.WorkspaceID)
	first := beginEvents(t, s)
	if err := Reconcile(t.Context(), first, scope, []Item{item("first")}, nil); err != nil {
		t.Fatal(err)
	}
	var firstID int64
	if err := first.QueryRow(t.Context(), `SELECT max(id) FROM resource_events WHERE workspace_id=$1`, scope.WorkspaceID).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	second := beginEvents(t, s)
	go func() {
		err := Reconcile(t.Context(), second, other, []Item{item("second")}, nil)
		if err == nil {
			err = second.Commit(t.Context())
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("second transaction bypassed uncommitted event: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if err := first.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second commit stuck")
	}
	got := events(t, s, other, firstID)
	if len(got) != 1 {
		t.Fatalf("replay skipped later commit: %+v", got)
	}
}
