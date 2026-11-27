// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCompatibilityReadIsolation(t *testing.T) {
	s, scope := setup(t)
	if _, err := s.db.Exec(t.Context(), `UPDATE cloud_accounts SET provider='alibaba' WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	scope.Provider = "alibaba"
	for _, spec := range []struct{ service, kind, region string }{
		{"compute", "instance", "cn-hangzhou"}, {"eds.desktops", "desktop", "cn-hangzhou"}, {"eds.users", "directory_user", "cn-shanghai"}, {"oss.buckets", "bucket", "cn-hangzhou"}, {"ecs.security_groups", "security_group", "cn-hangzhou"},
	} {
		target := scope
		target.Service, target.Kind, target.Region = spec.service, spec.kind, spec.region
		reconcile(t, s, target, []Item{{ProviderID: "visible", Name: "visible", State: "running", Details: json.RawMessage(`{"id":"visible"}`)}})
	}
	allowed := []int64{scope.AccountID}
	check := func(t *testing.T, workspace int64, accounts []int64, want int) {
		t.Helper()
		raw, err := s.Instances(t.Context(), workspace, accounts, "", "", "", 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &page); err != nil || len(page.Data) != want {
			t.Fatalf("instances=%s err=%v", raw, err)
		}
		summary, err := s.Summary(t.Context(), workspace, accounts)
		if err != nil || summary["instances"] != want {
			t.Fatalf("summary=%v err=%v", summary, err)
		}
		for _, service := range []string{"eds.desktops", "eds.users", "oss.buckets", "ecs.security_groups"} {
			region := ""
			if service == "eds.desktops" {
				region = "all"
			}
			if service == "eds.users" {
				region = "cn-hangzhou"
			}
			data, found, err := s.ServiceSnapshot(t.Context(), workspace, accounts, service, region)
			encoded, _ := json.Marshal(data["data"])
			var items []json.RawMessage
			decodeErr := json.Unmarshal(encoded, &items)
			if err != nil || decodeErr != nil || found != (want > 0) || len(items) != want {
				t.Fatalf("%s=%v found=%v err=%v decode=%v", service, data, found, err, decodeErr)
			}
		}
		for _, query := range []func() (pgx.Rows, error){func() (pgx.Rows, error) { return s.ReportAccounts(t.Context(), workspace, accounts) }, func() (pgx.Rows, error) { return s.ReportInstances(t.Context(), workspace, accounts) }, func() (pgx.Rows, error) { return s.ReportServices(t.Context(), workspace, accounts) }} {
			rows, err := query()
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (want > 0) {
				t.Fatalf("report present=%v want=%d err=%v", present, want, err)
			}
		}
	}
	t.Run("authorized", func(t *testing.T) { check(t, scope.WorkspaceID, allowed, 1) })
	t.Run("empty-account-set", func(t *testing.T) { check(t, scope.WorkspaceID, []int64{}, 0) })
	t.Run("inaccessible-account", func(t *testing.T) { check(t, scope.WorkspaceID, []int64{scope.AccountID + 100000}, 0) })
	t.Run("foreign-workspace", func(t *testing.T) { check(t, scope.WorkspaceID+100000, allowed, 0) })
	page, err := s.List(t.Context(), scope.WorkspaceID, allowed, Filter{Kind: "instance"}, "", 50)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("list: %v %v", page, err)
	}
	id := page.Data[0].ID
	if _, err := s.db.Exec(t.Context(), `UPDATE cloud_accounts SET provider='aws' WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), scope.WorkspaceID, allowed, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old provider detail: %v", err)
	}
	page, err = s.List(t.Context(), scope.WorkspaceID, allowed, Filter{}, "", 50)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("old provider list: %v %v", page, err)
	}
	raw, err := s.Instances(t.Context(), scope.WorkspaceID, allowed, "", "", "", 50, 0)
	if err != nil || !strings.Contains(string(raw), `"data" : []`) {
		t.Fatalf("old provider instances: %s %v", raw, err)
	}
	// Account configuration remains visible; its retired provider's resources do not.
	summary, err := s.Summary(t.Context(), scope.WorkspaceID, allowed)
	if err != nil || summary["instances"] != 0 || summary["accounts"] != 1 {
		t.Fatalf("old provider summary: %v %v", summary, err)
	}
}

func TestInstanceSearchUsesTrigramIndexes(t *testing.T) {
	s, scope := setup(t)
	if _, err := s.db.Exec(t.Context(), `INSERT INTO resources(workspace_id,account_id,provider,service,kind,region,provider_id,name)
 SELECT $1,$2,'aws','compute','instance','us-east-1','i-'||n,CASE WHEN n=99999 THEN 'needle-only-match' ELSE 'ordinary-'||n END FROM generate_series(1,100000) n`, scope.WorkspaceID, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(t.Context(), `ANALYZE resources`); err != nil {
		t.Fatal(err)
	}
	query, args := instanceListQuery(scope.WorkspaceID, []int64{scope.AccountID}, "needle-only-match", "", "", 50, 0)
	rows, err := s.db.Query(t.Context(), "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "resources_name_trgm_idx") || !strings.Contains(plan.String(), "resources_provider_id_trgm_idx") {
		t.Fatalf("actual compatibility query did not use trigram indexes:\n%s", plan.String())
	}
	t.Log("compatibility search EXPLAIN uses resources_name_trgm_idx and resources_provider_id_trgm_idx")
	raw, err := s.Instances(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, "needle-only-match", "", "", 50, 0)
	var page struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal(raw, &page) != nil || len(page.Data) != 1 || page.Data[0].Name != "needle-only-match" {
		t.Fatalf("search: %s %v", raw, err)
	}
}

func TestEmptyCatalogScopeFreshness(t *testing.T) {
	s, scope := setup(t)
	if _, err := s.db.Exec(t.Context(), `INSERT INTO sync_state(account_id,service,region,"interval",last_success_at) VALUES($1,'eds.desktops','cn-hangzhou',interval '5 minutes',clock_timestamp())`, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	data, found, err := s.ServiceSnapshot(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, "eds.desktops", "all")
	if err != nil || !found || data["fetched_at"] == nil || string(data["data"].(json.RawMessage)) != "[]" {
		t.Fatalf("successful empty scope: %v %v", data, err)
	}
	freshness, err := s.ServiceFreshness(t.Context(), scope.WorkspaceID, []int64{scope.AccountID})
	if err != nil || freshness == nil {
		t.Fatalf("empty service freshness: %v %v", freshness, err)
	}
	freshness, err = s.ServiceFreshness(t.Context(), scope.WorkspaceID+1, []int64{scope.AccountID})
	if err != nil || freshness != nil {
		t.Fatalf("foreign service freshness: %v %v", freshness, err)
	}
	if _, err := s.db.Exec(t.Context(), `INSERT INTO sync_state(account_id,service,region,"interval",last_error) VALUES($1,'eds.desktops','cn-beijing',interval '5 minutes','Collection failed')`, scope.AccountID); err != nil {
		t.Fatal(err)
	}
	data, found, err = s.ServiceSnapshot(t.Context(), scope.WorkspaceID, []int64{scope.AccountID}, "eds.desktops", "all")
	if err != nil || !found || data["fetched_at"] != (*time.Time)(nil) || len(data["failed_regions"].([]string)) != 1 {
		t.Fatalf("missing region freshness: %v %v", data, err)
	}
}
