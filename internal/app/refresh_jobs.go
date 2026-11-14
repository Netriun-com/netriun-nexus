// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"github.com/jackc/pgx/v5"
	syncer "github.com/netriun/nexus/internal/sync"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Still used by the EDS/OSS/security-group Valkey runners until T14.
const refreshJobTTL = time.Hour

type inventoryRefreshJob = syncer.Refresh

func validRefreshJobID(value string) bool {
	if len(value) < 20 || len(value) > 100 {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func (a *App) refreshInventory(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	allowed, err := a.Policy.AccessibleAccountIDs(r.Context(), u, CapabilityAccountView)
	if err != nil {
		dbError(w, err)
		return
	}
	ids, ok := a.requestedAccountIDs(w, r, allowed)
	if !ok {
		return
	}
	if len(ids) == 0 {
		write(w, http.StatusOK, syncer.Refresh{Status: "current", AccountIDs: []int64{}, FailedAccounts: []syncer.RefreshFailure{}})
		return
	}
	region := strings.TrimSpace(r.URL.Query().Get("region"))
	if region == "all" {
		region = ""
	}
	job, err := a.Sync.RefreshCompute(r.Context(), u.WorkspaceID, ids, region)
	if errors.Is(err, syncer.ErrNoScopes) {
		problem(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "Could not queue inventory refresh")
		return
	}
	a.audit(r, "inventory.refresh_queued", job.ID, map[string]any{"account_ids": ids, "region": region})
	write(w, http.StatusAccepted, job)
}

func (a *App) inventoryRefreshStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("jobID")
	if !validRefreshJobID(id) {
		problem(w, http.StatusBadRequest, "Invalid refresh job ID")
		return
	}
	u := current(r)
	allowed, err := a.Policy.AccessibleAccountIDs(r.Context(), u, CapabilityAccountView)
	if err != nil {
		dbError(w, err)
		return
	}
	job, err := a.Sync.RefreshStatus(r.Context(), u.WorkspaceID, id, allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, http.StatusNotFound, "Refresh job not found")
		return
	}
	if errors.Is(err, syncer.ErrAccountAccess) {
		problem(w, http.StatusForbidden, "Cloud account access required")
		return
	}
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, job)
}
