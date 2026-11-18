// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setup(t *testing.T) (*Store, Scope) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for resource store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Database != "ccmp_test" {
		t.Fatal("use isolated ccmp_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{fmt.Sprintf("resources_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	db, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tx, err := db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), "SELECT pg_advisory_xact_lock(771903)"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../app/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), string(raw)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	return store, newScope(t, store, 0)
}
func newScope(t *testing.T, s *Store, workspace int64) Scope {
	t.Helper()
	if workspace == 0 {
		if err := s.db.QueryRow(t.Context(), `INSERT INTO workspaces(name,slug) VALUES('Resources',$1) RETURNING id`, fmt.Sprintf("resources-%d", time.Now().UnixNano())).Scan(&workspace); err != nil {
			t.Fatal(err)
		}
	}
	scope := Scope{WorkspaceID: workspace, Provider: "aws", Service: "compute", Kind: "instance", Region: "us-east-1"}
	if err := s.db.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,$2,'aws','test',ARRAY['us-east-1']) RETURNING id`, workspace, fmt.Sprintf("Account-%d", time.Now().UnixNano())).Scan(&scope.AccountID); err != nil {
		t.Fatal(err)
	}
	return scope
}
func reconcile(t *testing.T, s *Store, scope Scope, items []Item, edges ...Relation) {
	t.Helper()
	tx, err := s.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := Reconcile(t.Context(), tx, scope, items, edges); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func listing(t *testing.T, s *Store, scope Scope) []Resource {
	t.Helper()
	p, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, Filter{Kind: scope.Kind, Service: scope.Service, Region: scope.Region}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return p.Data
}
func item(id string) Item {
	return Item{ProviderID: id, Name: id, State: "running", Tags: json.RawMessage(`{"a":1,"b":2}`), Summary: json.RawMessage(`{"type":"vm"}`), Details: json.RawMessage(`{"private":"details-only"}`)}
}

func TestReconcileVersionAndScope(t *testing.T) {
	s, scope := setup(t)
	original := item("one")
	reconcile(t, s, scope, []Item{original, item("two")})
	first := listing(t, s, scope)[0]
	equivalent := original
	equivalent.Tags = json.RawMessage(`{"b":2,"a":1.0}`)
	reconcile(t, s, scope, []Item{equivalent, item("two")})
	unchanged := listing(t, s, scope)[0]
	if unchanged.ID != first.ID || unchanged.Version != 1 || !unchanged.CreatedAt.Equal(first.CreatedAt) || !unchanged.UpdatedAt.Equal(first.UpdatedAt) || !unchanged.SeenAt.After(first.SeenAt) {
		t.Fatalf("unchanged object churned: before=%+v after=%+v", first, unchanged)
	}
	changed := equivalent
	for i, field := range []string{"name", "state", "tags", "summary", "details"} {
		switch field {
		case "name":
			changed.Name = "renamed"
		case "state":
			changed.State = "stopped"
		case "tags":
			changed.Tags = json.RawMessage(`{"a":3}`)
		case "summary":
			changed.Summary = json.RawMessage(`{"type":"changed"}`)
		case "details":
			changed.Details = json.RawMessage(`{"private":"new"}`)
		}
		reconcile(t, s, scope, []Item{changed, item("two")})
		got, err := s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Version != int64(i+2) || !got.UpdatedAt.After(unchanged.UpdatedAt) || !got.CreatedAt.Equal(first.CreatedAt) {
			t.Fatalf("%s did not version correctly: %+v", field, got)
		}
		unchanged = got.Resource
	}
	changed.Details = json.RawMessage(`null`)
	reconcile(t, s, scope, []Item{changed, item("two")})
	withNull, err := s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, first.ID)
	if err != nil || withNull.Version != 7 || string(withNull.Details) != "null" {
		t.Fatalf("JSON null update: %+v %v", withNull, err)
	}
	reconcile(t, s, scope, []Item{changed, item("two")})
	withNull, err = s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, first.ID)
	if err != nil || withNull.Version != 7 {
		t.Fatalf("unchanged JSON null version: %+v %v", withNull, err)
	}
	// Every dimension bounds unseen deletion, including accounts and workspaces.
	otherAccount := newScope(t, s, scope.WorkspaceID)
	otherWorkspace := newScope(t, s, 0)
	region, service, kind := scope, scope, scope
	region.Region = "us-west-2"
	service.Service = "network"
	kind.Kind = "volume"
	for _, other := range []Scope{otherAccount, otherWorkspace, region, service, kind} {
		reconcile(t, s, other, []Item{item("other")})
	}
	reconcile(t, s, scope, []Item{changed})
	if got := listing(t, s, scope); len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("unseen delete: %+v", got)
	}
	reconcile(t, s, scope, nil)
	if got := listing(t, s, scope); len(got) != 0 {
		t.Fatalf("empty complete scope: %+v", got)
	}
	for _, other := range []Scope{otherAccount, otherWorkspace, region, service, kind} {
		if got := listing(t, s, other); len(got) != 1 {
			t.Fatalf("deleted outside scope %+v: %+v", other, got)
		}
	}
}

func TestFailedScopeAndCallerRollback(t *testing.T) {
	s, scope := setup(t)
	reconcile(t, s, scope, []Item{item("keep")})
	before := listing(t, s, scope)[0]
	// Simulate the future handler discovering a failed page or losing its lease:
	// no commit means neither upserts nor deletes from Reconcile become visible.
	tx, err := s.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(t.Context(), tx, scope, []Item{item("partial")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, s, scope); len(got) != 1 || got[0].ID != before.ID {
		t.Fatalf("store committed caller transaction: %+v", got)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, s, scope); len(got) != 1 || got[0].ID != before.ID || got[0].Version != before.Version {
		t.Fatalf("rollback lost data: %+v", got)
	}
	for _, items := range [][]Item{{item("duplicate"), item("duplicate")}, {{ProviderID: "bad", Details: json.RawMessage(`{`)}}} {
		tx, err := s.db.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		err = Reconcile(t.Context(), tx, scope, items, nil)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad input accepted: %v", err)
		}
		_ = tx.Rollback(t.Context())
	}
	wrong := scope
	wrong.WorkspaceID++
	tx, err = s.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(t.Context(), tx, wrong, nil, nil); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong workspace write: %v", err)
	}
	_ = tx.Rollback(t.Context())
	if got := listing(t, s, scope); len(got) != 1 || got[0].ID != before.ID {
		t.Fatalf("failed scope changed data: %+v", got)
	}
}

func ref(scope Scope, id string) Reference {
	return Reference{Service: scope.Service, Region: scope.Region, Kind: scope.Kind, ProviderID: id}
}
func TestRelationsOwnershipReplacementAndCascade(t *testing.T) {
	s, scope := setup(t)
	target := scope
	target.Service = "network"
	target.Kind = "subnet"
	reconcile(t, s, target, []Item{item("subnet")})
	targetID := listing(t, s, target)[0].ID
	edge := Relation{From: ref(scope, "vm"), To: ref(target, "subnet"), Relation: "in_subnet"}
	missing := edge
	missing.To.ProviderID = "not-discovered"
	reconcile(t, s, scope, []Item{item("vm")}, edge, edge, missing)
	vm := listing(t, s, scope)[0]
	detail, err := s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, vm.ID)
	if err != nil || len(detail.Relations) != 1 || detail.Relations[0].ID != targetID || detail.Relations[0].Kind != "subnet" {
		t.Fatalf("outgoing: %+v %v", detail, err)
	}
	// Reconciling the target's outgoing edges must preserve incoming edges.
	reconcile(t, s, target, []Item{item("subnet")})
	detail, err = s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, targetID)
	if err != nil || len(detail.Relations) != 1 || detail.Relations[0].ID != vm.ID {
		t.Fatalf("incoming: %+v %v", detail, err)
	}
	reconcile(t, s, scope, []Item{item("vm")})
	detail, err = s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, vm.ID)
	if err != nil || len(detail.Relations) != 0 || detail.Version != vm.Version {
		t.Fatalf("edge replacement/version: %+v %v", detail, err)
	}
	reconcile(t, s, scope, []Item{item("vm")}, edge)
	reconcile(t, s, target, nil)
	detail, err = s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, vm.ID)
	if err != nil || len(detail.Relations) != 0 {
		t.Fatalf("target cascade: %+v %v", detail, err)
	}
	reconcile(t, s, target, []Item{item("subnet")})
	reconcile(t, s, scope, []Item{item("vm")}, edge)
	if _, err := s.db.Exec(t.Context(), `DELETE FROM cloud_accounts WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	var resources, relations int
	if err := s.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM resources),(SELECT count(*) FROM resource_relations)`).Scan(&resources, &relations); err != nil || resources != 0 || relations != 0 {
		t.Fatalf("account cascade: %d %d %v", resources, relations, err)
	}
}

func TestReadIsolationAndSummaries(t *testing.T) {
	s, scope := setup(t)
	hidden := newScope(t, s, scope.WorkspaceID)
	foreign := newScope(t, s, 0)
	for _, sc := range []Scope{scope, hidden, foreign} {
		reconcile(t, s, sc, []Item{item("same-provider-id")})
	}
	ownID, hiddenID, foreignID := listing(t, s, scope)[0].ID, listing(t, s, hidden)[0].ID, listing(t, s, foreign)[0].ID
	// Even corrupted/directly inserted cross-account edges cannot expose names.
	if _, err := s.db.Exec(t.Context(), `INSERT INTO resource_relations(from_id,to_id,relation) VALUES($1,$2,'hidden'),($3,$1,'foreign')`, ownID, hiddenID, foreignID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ws       int64
		accounts []int64
		id       int64
	}{{scope.WorkspaceID, []int64{scope.AccountID}, hiddenID}, {scope.WorkspaceID, []int64{foreign.AccountID}, foreignID}, {foreign.WorkspaceID, []int64{scope.AccountID}, ownID}, {scope.WorkspaceID, nil, ownID}} {
		if _, err := s.Get(t.Context(), tc.ws, tc.accounts, tc.id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("inaccessible Get: %+v %v", tc, err)
		}
	}
	page, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID, foreign.AccountID}, Filter{}, "", 50)
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != ownID {
		t.Fatalf("List scope: %+v %v", page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "details") || !strings.Contains(string(raw), "summary") {
		t.Fatalf("list leaked details: %s", raw)
	}
	detail, err := s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, ownID)
	if err != nil || len(detail.Relations) != 0 || !strings.Contains(string(detail.Details), "details-only") {
		t.Fatalf("detail isolation: %+v %v", detail, err)
	}
	detail, err = s.Get(t.Context(), scope.WorkspaceID, []int64{scope.AccountID, hidden.AccountID}, ownID)
	if err != nil || len(detail.Relations) != 1 || detail.Relations[0].ID != hiddenID {
		t.Fatalf("accessible relation missing: %+v %v", detail, err)
	}
	page, err = s.List(t.Context(), scope.WorkspaceID, nil, Filter{}, "", 50)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("nil accounts granted access: %+v %v", page, err)
	}
}
