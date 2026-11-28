// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fail at the database boundary if any exercised code writes a retired table.
func rejectLegacyInventoryWrites(t *testing.T, a *App) {
	t.Helper()
	_, err := a.DB.Exec(t.Context(), `DROP TRIGGER IF EXISTS reject_legacy_instances ON instances; DROP TRIGGER IF EXISTS reject_legacy_snapshots ON service_snapshots; CREATE OR REPLACE FUNCTION reject_legacy_inventory_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'deprecated inventory write: %',TG_TABLE_NAME; END $$;
 CREATE TRIGGER reject_legacy_instances BEFORE INSERT OR UPDATE OR DELETE ON instances FOR EACH ROW EXECUTE FUNCTION reject_legacy_inventory_write();
 CREATE TRIGGER reject_legacy_snapshots BEFORE INSERT OR UPDATE OR DELETE ON service_snapshots FOR EACH ROW EXECUTE FUNCTION reject_legacy_inventory_write();`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := a.DB.Exec(context.Background(), `DROP TRIGGER IF EXISTS reject_legacy_instances ON instances; DROP TRIGGER IF EXISTS reject_legacy_snapshots ON service_snapshots; DROP FUNCTION IF EXISTS reject_legacy_inventory_write()`)
		if err != nil {
			t.Error(err)
		}
	})
}

func testCompatibilityLists100000(t *testing.T, a *App) {
	ctx := t.Context()
	var ws, account int64
	if err := a.DB.QueryRow(ctx, `INSERT INTO workspaces(name,slug) VALUES('T23 performance','t23-performance') RETURNING id`).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	defer a.DB.Exec(context.Background(), `DELETE FROM workspaces WHERE id=$1`, ws)
	if err := a.DB.QueryRow(ctx, `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'T23 Alibaba','alibaba','unused',ARRAY['cn-hangzhou']) RETURNING id`, ws).Scan(&account); err != nil {
		t.Fatal(err)
	}
	u := User{WorkspaceID: ws, Role: "admin"}
	if err := a.DB.QueryRow(ctx, `INSERT INTO users(workspace_id,username,email,password_hash,role) VALUES($1,'t23-perf','t23-perf@example.com','unused','admin') RETURNING id,session_version`, ws).Scan(&u.ID, &u.Version); err != nil {
		t.Fatal(err)
	}
	token, ok := a.establishSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), u)
	if !ok {
		t.Fatal("session")
	}
	raw, err := os.ReadFile("../../scripts/seed-resource-lists.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := a.DB.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `SELECT set_config('nexus.seed_workspace',$1,false),set_config('nexus.seed_account',$2,false)`, strconv.FormatInt(ws, 10), strconv.FormatInt(account, 10)); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, string(raw))
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM resources WHERE workspace_id=$1 AND account_id=$2`, ws, account).Scan(&count); err != nil || count != 100000 {
		t.Fatalf("seed count=%d err=%v", count, err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	for _, path := range []string{
		"/api/v1/instances", "/api/v1/instances?q=019999", "/api/v1/instances?q=203.0.113.10", "/api/v1/accounts", "/api/v1/summary", "/api/v1/reports/overview", "/api/v1/resources",
		fmt.Sprintf("/api/v1/accounts/%d/eds/desktops?region=all", account), fmt.Sprintf("/api/v1/accounts/%d/eds/desktops?region=cn-hangzhou", account), fmt.Sprintf("/api/v1/accounts/%d/eds/regions", account), fmt.Sprintf("/api/v1/accounts/%d/eds/users?region=cn-hangzhou", account), fmt.Sprintf("/api/v1/accounts/%d/oss/buckets", account), fmt.Sprintf("/api/v1/accounts/%d/ecs/security-groups", account),
	} {
		t.Run(path, func(t *testing.T) {
			var max, total time.Duration
			for i := range 3 {
				r, err := http.NewRequest("GET", server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Authorization", "Bearer "+token)
				start := time.Now()
				response, err := server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				elapsed := time.Since(start)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if response.StatusCode != 200 {
					t.Fatalf("status %d: %s", response.StatusCode, body)
				}
				if !json.Valid(body) {
					t.Fatal("invalid JSON")
				}
				if strings.Contains(path, "/summary") && !strings.Contains(string(body), `"instances":20000`) {
					t.Fatalf("counts: %s", body)
				}
				if elapsed >= time.Second {
					t.Fatalf("request %d exceeded one second: %s", i, elapsed)
				}
				total += elapsed
				if elapsed > max {
					max = elapsed
				}
			}
			t.Logf("100000 resources; authenticated HTTP %s: mean=%s max=%s (3 requests)", path, total/3, max)
		})
	}
}
