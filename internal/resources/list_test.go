// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFiltersAndLiteralSearch(t *testing.T) {
	s, scope := setup(t)
	a, b := item("id-a"), item("special-provider-id")
	a.Name = "Alpha_100%"
	b.Name = "Beta"
	b.State = "stopped"
	reconcile(t, s, scope, []Item{a, b})
	region, service, kind := scope, scope, scope
	region.Region = "west"
	service.Service = "other"
	kind.Kind = "volume"
	for _, sc := range []Scope{region, service, kind} {
		reconcile(t, s, sc, []Item{item("extra")})
	}
	for _, tc := range []struct {
		filter Filter
		want   int
	}{
		{Filter{}, 5}, {Filter{Kind: "volume"}, 1}, {Filter{Service: "other"}, 1}, {Filter{Region: "west"}, 1},
		{Filter{State: "stopped"}, 1}, {Filter{Q: "ALPHA"}, 1}, {Filter{Q: "special-provider"}, 1},
		{Filter{Q: "_100%"}, 1}, {Filter{Q: "%"}, 1}, {Filter{Q: `\`}, 0},
		{Filter{Kind: "instance", Service: "compute", Region: "us-east-1", State: "running", Q: "alpha"}, 1},
	} {
		got, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, tc.filter, "", 50)
		if err != nil || len(got.Data) != tc.want {
			t.Fatalf("filter %+v: %d want %d (%v)", tc.filter, len(got.Data), tc.want, err)
		}
	}
}

func TestCursorInsertionStability(t *testing.T) {
	s, scope := setup(t)
	items := make([]Item, 20)
	for i := range items {
		items[i] = item(fmt.Sprintf("id-%02d", i))
		items[i].Name = fmt.Sprintf("name-%02d", i/2)
	}
	reconcile(t, s, scope, items)
	initial := listing(t, s, scope)
	filter := Filter{Kind: "instance"}
	cursor := ""
	seen := map[int64]bool{}
	var got []int64
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		page, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, filter, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Data {
			if seen[r.ID] {
				t.Fatalf("duplicate %d", r.ID)
			}
			seen[r.ID] = true
			got = append(got, r.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		// Insert before the cursor, at the same name, and after all existing names.
		for i, name := range []string{"aaa", page.Data[len(page.Data)-1].Name, "zzz"} {
			v := item(fmt.Sprintf("insert-%d-%d", pageNumber, i))
			v.Name = name
			items = append(items, v)
		}
		reconcile(t, s, scope, items)
	}
	if len(got) != len(initial) {
		t.Fatalf("paging gaps/new inserts: got %d want %d", len(got), len(initial))
	}
	for i, r := range initial {
		if got[i] != r.ID {
			t.Fatalf("page order/gap at %d: %d want %d", i, got[i], r.ID)
		}
	}
	if len(listing(t, s, scope)) != len(items) {
		t.Fatal("new traversal omitted inserted rows")
	}
	for _, bad := range []string{"bad!", "e30", strings.Repeat("a", 16385)} {
		if _, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, filter, bad, 3); !errors.Is(err, ErrCursor) {
			t.Fatalf("invalid cursor accepted: %v", err)
		}
	}
	if _, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, Filter{Kind: "volume"}, cursor, 3); !errors.Is(err, ErrCursor) {
		t.Fatalf("changed filter accepted: %v", err)
	}
	if _, err := s.List(t.Context(), scope.WorkspaceID+1, []int64{scope.AccountID}, filter, cursor, 3); !errors.Is(err, ErrCursor) {
		t.Fatalf("changed workspace accepted: %v", err)
	}
	if _, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID + 1}, filter, cursor, 3); !errors.Is(err, ErrCursor) {
		t.Fatalf("changed accounts accepted: %v", err)
	}
}

func TestSearchUsesTrigramIndexes(t *testing.T) {
	s, scope := setup(t)
	if _, err := s.db.Exec(t.Context(), `INSERT INTO resources(workspace_id,account_id,provider,service,kind,region,provider_id,name)
 SELECT $1,$2,'aws','compute','instance','us-east-1','rid-'||i,
 CASE WHEN i=42 THEN 'needle-special-resource' ELSE md5(i::text) END FROM generate_series(1,100000) AS i`, scope.WorkspaceID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(t.Context(), "ANALYZE resources"); err != nil {
		t.Fatal(err)
	}
	var ceiling int64
	if err := s.db.QueryRow(t.Context(), `SELECT max(id) FROM resources WHERE workspace_id=$1 AND account_id=ANY($2)`, scope.WorkspaceID, []int64{scope.AccountID}).Scan(&ceiling); err != nil {
		t.Fatal(err)
	}
	query, args := listQuery(scope.WorkspaceID, []int64{scope.AccountID}, Filter{Q: "needle-special"}, pageCursor{Ceiling: ceiling}, 51)
	rows, err := s.db.Query(t.Context(), "EXPLAIN (ANALYZE, BUFFERS) "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n")
	t.Logf("100,000 resource q search (normal planner settings):\n%s", plan)
	for _, index := range []string{"resources_name_trgm_idx", "resources_provider_id_trgm_idx"} {
		if !strings.Contains(plan, "Bitmap Index Scan on "+index) {
			t.Fatalf("trigram index not used: %s", plan)
		}
	}
	page, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, Filter{Q: "needle-special"}, "", 50)
	if err != nil || len(page.Data) != 1 || page.Data[0].ProviderID != "rid-42" {
		t.Fatalf("q search: %+v %v", page, err)
	}
	for _, tc := range []struct{ limit, want int }{{0, 50}, {1000, 200}, {1, 1}} {
		page, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, Filter{}, "", tc.limit)
		if err != nil || len(page.Data) != tc.want || page.NextCursor == "" {
			t.Fatalf("limit %d: %d %v", tc.limit, len(page.Data), err)
		}
	}
	if _, err := s.List(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, Filter{}, "", -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative limit: %v", err)
	}
}
