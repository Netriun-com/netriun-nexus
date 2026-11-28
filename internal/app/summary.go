// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/http"

	"github.com/netriun/nexus/internal/resources"
)

func (a *App) summary(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	ids, err := a.Policy.AccessibleAccountIDs(r.Context(), u, CapabilityAccountView)
	if err != nil {
		dbError(w, err)
		return
	}
	ids, ok := a.requestedAccountIDs(w, r, ids)
	if !ok {
		return
	}
	result, err := resources.New(a.DB).Summary(r.Context(), u.WorkspaceID, ids)
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, 200, result)
}
