// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	syncer "github.com/netriun/nexus/internal/sync"
	"net/http"
	"strings"
)

func (a *App) syncState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	switch service {
	case "", syncer.Compute, syncer.Desktops, syncer.Users, syncer.Buckets, syncer.SecurityGroups:
	default:
		problem(w, 400, "Invalid sync service")
		return
	}
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
	rows, err := a.Sync.States(r.Context(), u.WorkspaceID, ids, service)
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, 200, map[string]any{"data": rows})
}
