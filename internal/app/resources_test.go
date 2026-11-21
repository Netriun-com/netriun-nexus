// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/resources"
)

// Called inside TestIntegration so the real router, authentication, policy,
// PostgreSQL and Valkey are exercised with the same isolated services as CI.
func testResourcesAPI(t *testing.T, a *App) {
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	newWorkspace := func(slug string) int64 {
		t.Helper()
		var id int64
		if err := a.DB.QueryRow(ctx, `INSERT INTO workspaces(name,slug) VALUES($1,$1) RETURNING id`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := a.DB.Exec(context.Background(), "DELETE FROM workspaces WHERE id=$1", id); err != nil {
				t.Error(err)
			}
		})
		return id
	}
	workspace, otherWorkspace := newWorkspace("resource-api"), newWorkspace("resource-api-other")
	newUser := func(ws int64, name, role string) (int64, string) {
		t.Helper()
		u := User{WorkspaceID: ws, Role: role}
		if err := a.DB.QueryRow(ctx, `INSERT INTO users(workspace_id,username,email,password_hash,role) VALUES($1,$2,$2||'@example.com','unused',$3) RETURNING id,session_version`, ws, name, role).Scan(&u.ID, &u.Version); err != nil {
			t.Fatal(err)
		}
		token, ok := a.establishSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), u)
		if !ok {
			t.Fatal("create test session")
		}
		return u.ID, token
	}
	viewer, token := newUser(workspace, "resource-viewer", "user")
	exec(`INSERT INTO access_roles(workspace_id,key,name,permissions) VALUES($1,'catalog-viewer','Catalog viewer',ARRAY['account.view'])`, workspace)
	_, emptyToken := newUser(workspace, "resource-empty", "user")
	_, foreignToken := newUser(otherWorkspace, "resource-admin", "admin")
	newAccount := func(ws int64, provider, name string, grant bool) resources.Scope {
		t.Helper()
		scope := resources.Scope{WorkspaceID: ws, Provider: provider, Service: "compute", Region: "us-east-1", Kind: "instance"}
		if err := a.DB.QueryRow(ctx, `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,$2,$3,'unused',ARRAY['us-east-1']) RETURNING id`, ws, name, provider).Scan(&scope.AccountID); err != nil {
			t.Fatal(err)
		}
		if grant {
			exec(`INSERT INTO account_access_assignments(workspace_id,principal_type,user_id,cloud_account_id,role_id) SELECT $1,'user',$2,$3,id FROM access_roles WHERE workspace_id=$1 AND key='catalog-viewer'`, ws, viewer, scope.AccountID)
		}
		return scope
	}
	first := newAccount(workspace, "aws", "first", true)
	second := newAccount(workspace, "aws", "second", true)
	denied := newAccount(workspace, "aws", "denied", false)
	foreign := newAccount(otherWorkspace, "aws", "foreign", false)
	alibaba := newAccount(workspace, "alibaba", "alibaba", true)
	seed := func(scope resources.Scope, items []resources.Item, edges ...resources.Relation) {
		t.Helper()
		tx, err := a.DB.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err := resources.Reconcile(ctx, tx, scope, items, edges); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	item := func(id, name, state string) resources.Item {
		return resources.Item{ProviderID: id, Name: name, State: state, Summary: json.RawMessage(`{"size":"small"}`), Details: json.RawMessage(`{"private_detail":true}`)}
	}
	seed(first, []resources.Item{item("i-alpha", "Alpha", "running"), item("i-twin", "Alpha", "stopped"), item("i-literal", "100%_literal", "running")})
	west := first
	west.Region = "us-west-2"
	seed(west, []resources.Item{item("i-west", "West", "running")})
	network := first
	network.Service = "network"
	network.Kind = "network"
	seed(network, []resources.Item{item("vpc-one", "Network", "available")}, resources.Relation{From: resources.Reference{Service: "network", Region: network.Region, Kind: "network", ProviderID: "vpc-one"}, To: resources.Reference{Service: "compute", Region: first.Region, Kind: "instance", ProviderID: "i-alpha"}, Relation: "contains"})
	seed(second, []resources.Item{item("i-second", "Beta", "running")})
	seed(denied, []resources.Item{item("i-denied", "Hidden", "running")})
	seed(foreign, []resources.Item{item("i-foreign", "Foreign", "running")})
	seed(alibaba, []resources.Item{item("ali-one", "Alibaba", "running")})
	h := a.Handler()
	request := func(path, auth string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/resources"+path, nil)
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("GET resources%s: got %d want %d: %s", path, w.Code, status, w.Body.String())
		}
		if status >= 400 {
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("expected a plain error message: %s", w.Body.String())
			}
		}
		return w
	}
	page := func(query, auth string) resources.Page {
		t.Helper()
		w := request(query, auth, 200)
		var raw struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if raw.Data == nil {
			t.Fatalf("data must be an array: %s", w.Body.String())
		}
		for _, row := range raw.Data {
			if _, ok := row["details"]; ok {
				t.Fatal("list returned details")
			}
			if _, ok := row["summary"]; !ok {
				t.Fatal("list omitted summary")
			}
		}
		var out resources.Page
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	request("", "", 401)
	request("/1", "", 401)
	all := page("", token)
	if len(all.Data) != 7 {
		t.Fatalf("accessible list: %+v", all)
	}
	for _, r := range all.Data {
		if r.WorkspaceID != workspace || !slices.Contains([]int64{first.AccountID, second.AccountID, alibaba.AccountID}, r.AccountID) {
			t.Fatalf("list leaked an inaccessible resource: %+v", r)
		}
	}
	if got := page("", emptyToken); len(got.Data) != 0 || got.NextCursor != "" {
		t.Fatalf("empty access: %+v", got)
	}
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"kind=network", []string{"vpc-one"}},
		{"service=network", []string{"vpc-one"}},
		{"region=us-west-2", []string{"i-west"}},
		{"state=stopped", []string{"i-twin"}},
		{"q=ALPHA", []string{"i-alpha", "i-twin"}},
		{"q=i-second", []string{"i-second"}},
		{"q=" + url.QueryEscape("%_"), []string{"i-literal"}},
		{fmt.Sprintf("account_ids=%d", second.AccountID), []string{"i-second"}},
		{fmt.Sprintf("account_id=%d", second.AccountID), []string{"i-second"}},
		{"kind=instance&service=compute&region=us-east-1&state=running&q=Alpha", []string{"i-alpha"}},
		{"kind=unknown", []string{}},
	} {
		got := []string{}
		for _, r := range page("?"+tc.query, token).Data {
			got = append(got, r.ProviderID)
		}
		slices.Sort(got)
		if !reflect.DeepEqual(got, tc.ids) {
			t.Fatalf("%s: got %v want %v", tc.query, got, tc.ids)
		}
	}
	for _, q := range []string{"limit=0", "limit=-1", "limit=201", "limit=nope", "limit=1.5", "limit=", "limit=9999999999999999999999", "account_ids=bad", "account_ids=0", fmt.Sprintf("account_ids=%d,", first.AccountID), "account_ids=" + strings.Repeat("1,", 100) + "1", fmt.Sprintf("account_ids=%d,%d", first.AccountID, alibaba.AccountID)} {
		request("?"+q, token, 400)
	}
	for _, id := range []int64{denied.AccountID, foreign.AccountID, 999999999} {
		request(fmt.Sprintf("?account_ids=%d", id), token, 403)
	}
	request(fmt.Sprintf("?account_ids=%d,%d", first.AccountID, denied.AccountID), token, 403)
	request(fmt.Sprintf("?account_ids=%d", first.AccountID), emptyToken, 403)
	// Both directions carry the related endpoint's kind/name and details stay on Get.
	var alphaID int64
	for _, r := range all.Data {
		if r.ProviderID != "i-alpha" && r.ProviderID != "vpc-one" {
			continue
		}
		var detail resources.Detail
		if err := json.Unmarshal(request(fmt.Sprintf("/%d", r.ID), token, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		var fields map[string]bool
		if err := json.Unmarshal(detail.Details, &fields); err != nil {
			t.Fatal(err)
		}
		if !fields["private_detail"] || len(detail.Relations) != 1 {
			t.Fatalf("detail: %+v", detail)
		}
		edge := detail.Relations[0]
		if r.ProviderID == "i-alpha" {
			alphaID = r.ID
			if edge.Kind != "network" || edge.Name != "Network" || edge.ToID != r.ID {
				t.Fatalf("incoming edge: %+v", edge)
			}
		} else if edge.Kind != "instance" || edge.Name != "Alpha" || edge.FromID != r.ID {
			t.Fatalf("outgoing edge: %+v", edge)
		}
	}
	for _, scope := range []resources.Scope{denied, foreign} {
		stored, err := a.Resources.List(ctx, scope.WorkspaceID, []int64{scope.AccountID}, resources.Filter{}, "", 50)
		if err != nil || len(stored.Data) != 1 {
			t.Fatalf("seed: %+v %v", stored, err)
		}
		request(fmt.Sprintf("/%d", stored.Data[0].ID), token, 404)
	}
	request(fmt.Sprintf("/%d", alphaID), foreignToken, 404)
	request(fmt.Sprintf("/%d", alphaID), emptyToken, 404)
	request("/999999999", token, 404)
	for _, id := range []string{"0", "-1", "bad", "9999999999999999999999"} {
		request("/"+id, token, 400)
	}
	// Keyset pagination across equal names, and cursor scope binding.
	firstPage := page("?limit=2", token)
	if len(firstPage.Data) != 2 || firstPage.NextCursor == "" {
		t.Fatal("missing cursor")
	}
	seen := []int64{}
	next := "?limit=2"
	for {
		p := page(next, token)
		for _, r := range p.Data {
			seen = append(seen, r.ID)
		}
		if p.NextCursor == "" {
			break
		}
		next = "?limit=2&cursor=" + url.QueryEscape(p.NextCursor)
		if len(seen) > len(all.Data) {
			t.Fatal("paging duplicated rows")
		}
	}
	want := []int64{}
	for _, r := range all.Data {
		want = append(want, r.ID)
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("paging: %v want %v", seen, want)
	}
	cursor := "cursor=" + url.QueryEscape(firstPage.NextCursor)
	request("?cursor=garbage", token, 400)
	request("?"+cursor+"&state=running", token, 400)
	request("?"+cursor+fmt.Sprintf("&account_ids=%d", first.AccountID), token, 400)
	request("?"+cursor, foreignToken, 400)
	request("?"+cursor, emptyToken, 400)
	exec("DELETE FROM account_access_assignments WHERE user_id=$1 AND cloud_account_id=$2", viewer, second.AccountID)
	request("?"+cursor, token, 400)
	// Restore permission for the scale fixture, then isolate it with account_ids.
	exec(`INSERT INTO account_access_assignments(workspace_id,principal_type,user_id,cloud_account_id,role_id) SELECT $1,'user',$2,$3,id FROM access_roles WHERE workspace_id=$1 AND key='catalog-viewer'`, workspace, viewer, second.AccountID)
	t.Run("100000Resources", func(t *testing.T) {
		scope := newAccount(workspace, "aws", "scale", true)
		items := make([]resources.Item, 100000)
		for i := range items {
			name := fmt.Sprintf("resource-%06d", i)
			if i%1000 == 0 {
				name = "needle-special-" + name
			}
			state := "running"
			if i%2 == 0 {
				state = "stopped"
			}
			items[i] = item(fmt.Sprintf("scale-%06d", i), name, state)
		}
		seed(scope, items)
		exec("ANALYZE resources")
		base := fmt.Sprintf("?account_ids=%d", scope.AccountID)
		if len(page(base, token).Data) != 50 || len(page(base+"&limit=200", token).Data) != 200 {
			t.Fatal("default/max page sizes")
		}
		for _, tc := range []struct{ name, query string }{
			{"filtered", base + "&kind=instance&service=compute&region=us-east-1&state=running"},
			{"q-search", base + "&q=needle-special"},
		} {
			var total, max time.Duration
			for i := 0; i < 10; i++ {
				start := time.Now()
				w := request(tc.query, token, 200)
				elapsed := time.Since(start)
				var p resources.Page
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if len(p.Data) != 50 || p.NextCursor == "" {
					t.Fatal("expected full page and cursor")
				}
				total += elapsed
				if elapsed > max {
					max = elapsed
				}
			}
			t.Logf("100000 resources, authenticated HTTP %s page (50 rows, 10 requests): mean=%s max=%s", tc.name, total/10, max)
			if max >= time.Second {
				t.Fatalf("%s page exceeded one second: %s", tc.name, max)
			}
		}
	})
}
