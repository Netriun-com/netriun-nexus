// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/netriun/nexus/internal/resources"
)

func (a *App) listResources(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 50
	if query.Has("limit") {
		var err error
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			problem(w, http.StatusBadRequest, "Limit must be an integer between 1 and 200")
			return
		}
	}
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
	filter := resources.Filter{Kind: query.Get("kind"), Service: query.Get("service"), Region: query.Get("region"), State: query.Get("state"), Q: query.Get("q")}
	page, err := a.Resources.List(r.Context(), u.WorkspaceID, ids, filter, query.Get("cursor"), limit)
	if errors.Is(err, resources.ErrCursor) {
		problem(w, http.StatusBadRequest, "Invalid cursor or changed filters/account access; restart from the first page")
		return
	}
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, page)
}

func (a *App) getResource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	u := current(r)
	ids, err := a.Policy.AccessibleAccountIDs(r.Context(), u, CapabilityAccountView)
	if err != nil {
		dbError(w, err)
		return
	}
	resource, err := a.Resources.Get(r.Context(), u.WorkspaceID, ids, id)
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, resource)
}
