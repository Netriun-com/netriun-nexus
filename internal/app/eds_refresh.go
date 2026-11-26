// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type edsRegionCount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Desktops  int    `json:"desktops"`
	Available bool   `json:"available"`
}

type edsRegionResponse struct {
	Data          []edsRegionCount `json:"data"`
	TotalDesktops int              `json:"total_desktops"`
}

type edsRefreshJob struct {
	ID            string     `json:"id"`
	WorkspaceID   int64      `json:"-"`
	RequestedBy   int64      `json:"-"`
	RequestedName string     `json:"-"`
	AccountID     int64      `json:"account_id"`
	Service       string     `json:"service"`
	Region        string     `json:"region"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func (a *App) edsSnapshotAccount(w http.ResponseWriter, r *http.Request, allowAll, requireRegion bool) (int64, string, bool) {
	accountID, ok := pathID(w, r, "id")
	if !ok {
		return 0, "", false
	}
	if !a.accountAccess(r, accountID, "view") {
		problem(w, http.StatusForbidden, "Cloud account access required")
		return 0, "", false
	}
	region := strings.TrimSpace(r.URL.Query().Get("region"))
	if requireRegion && (region == "" || region == "all" && !allowAll || region != "all" && !regionPattern.MatchString(region)) {
		problem(w, http.StatusBadRequest, "A valid Alibaba Cloud region is required")
		return 0, "", false
	}
	var provider string
	if err := a.DB.QueryRow(r.Context(), `SELECT provider FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, accountID, current(r).WorkspaceID).Scan(&provider); err != nil {
		dbError(w, err)
		return 0, "", false
	}
	if provider != "alibaba" {
		problem(w, http.StatusBadRequest, "The selected account is not an Alibaba Cloud connection")
		return 0, "", false
	}
	return accountID, region, true
}

func (a *App) edsCredentials(ctx context.Context, workspaceID, accountID int64) (Credentials, string, error) {
	credentials, err := a.credentials(ctx, workspaceID, accountID, "alibaba")
	if err != nil {
		return credentials, "", err
	}
	var seedRegion string
	err = a.DB.QueryRow(ctx, `SELECT COALESCE((SELECT min(region) FROM instances WHERE account_id=$1), NULLIF(regions[1],''), 'cn-hangzhou') FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, accountID, workspaceID).Scan(&seedRegion)
	return credentials, seedRegion, err
}

func (a *App) collectEDSDesktops(ctx context.Context, workspaceID, accountID int64, target string) error {
	return a.collectService(ctx, workspaceID, accountID, "eds.desktops", target)
}

func (a *App) collectEDSUsers(ctx context.Context, workspaceID, accountID int64, region string) error {
	return a.collectService(ctx, workspaceID, accountID, "eds.users", region)
}
