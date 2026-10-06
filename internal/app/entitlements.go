// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/http"

	"github.com/netriun/nexus/internal/entitlements"
)

type entitlementLimitStatus struct {
	Allowed int `json:"allowed"`
	Used    int `json:"used"`
}

func (a *App) entitlementStatus(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	snapshot := a.Entitlements.Snapshot(r.Context(), u.WorkspaceID)
	usage := map[entitlements.Limit]int{
		entitlements.LimitWorkspaces: 1,
	}
	var identities, accounts, retention int
	err := a.DB.QueryRow(r.Context(), `
SELECT
 (SELECT count(*) FROM users WHERE workspace_id=$1),
 (SELECT count(*) FROM cloud_accounts WHERE workspace_id=$1),
 (SELECT audit_retention_days FROM settings WHERE workspace_id=$1)
`, u.WorkspaceID).Scan(&identities, &accounts, &retention)
	if err != nil {
		dbError(w, err)
		return
	}
	usage[entitlements.LimitHumanIdentities] = identities
	usage[entitlements.LimitCloudAccounts] = accounts
	usage[entitlements.LimitAuditRetentionDays] = retention
	limits := make(map[entitlements.Limit]entitlementLimitStatus, len(snapshot.Limits))
	for key, allowed := range snapshot.Limits {
		limits[key] = entitlementLimitStatus{Allowed: allowed, Used: usage[key]}
	}
	write(w, http.StatusOK, map[string]any{
		"deployment_mode": snapshot.DeploymentMode,
		"edition":         snapshot.Edition,
		"status":          snapshot.Status,
		"features":        snapshot.Features,
		"limits":          limits,
	})
}

func entitlementProblem(w http.ResponseWriter, denial *entitlements.DecisionError) {
	write(w, http.StatusPaymentRequired, denial)
}

func (a *App) requireFeature(w http.ResponseWriter, r *http.Request, feature entitlements.Feature) bool {
	if denial := a.Entitlements.RequireFeature(r.Context(), current(r).WorkspaceID, feature); denial != nil {
		entitlementProblem(w, denial)
		return false
	}
	return true
}

func (a *App) requireLimit(w http.ResponseWriter, r *http.Request, limit entitlements.Limit, requested int) bool {
	if denial := a.Entitlements.RequireLimit(r.Context(), current(r).WorkspaceID, limit, requested); denial != nil {
		entitlementProblem(w, denial)
		return false
	}
	return true
}
