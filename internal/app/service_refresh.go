// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	syncer "github.com/netriun/nexus/internal/sync"
)

func (a *App) refreshEDS(w http.ResponseWriter, r *http.Request) {
	account, region, ok := a.edsSnapshotAccount(w, r, true, true)
	if !ok {
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service != "desktops" && service != "users" {
		problem(w, 400, "EDS refresh service must be desktops or users")
		return
	}
	if service == "users" && region == "all" {
		problem(w, 400, "EDS users require a directory region")
		return
	}
	a.queueServiceRefresh(w, r, account, "eds."+service, region)
}
func (a *App) refreshOSS(w http.ResponseWriter, r *http.Request) {
	if account, ok := a.ossAccount(w, r, "view"); ok {
		a.queueServiceRefresh(w, r, account, syncer.Buckets, "")
	}
}
func (a *App) refreshSecurityGroups(w http.ResponseWriter, r *http.Request) {
	if account, ok := a.securityGroupAccount(w, r, "view"); ok {
		a.queueServiceRefresh(w, r, account, syncer.SecurityGroups, "")
	}
}
func (a *App) queueServiceRefresh(w http.ResponseWriter, r *http.Request, account int64, service, region string) {
	job, err := a.Sync.RefreshService(r.Context(), current(r).WorkspaceID, []int64{account}, service, region)
	if errors.Is(err, syncer.ErrNoScopes) {
		problem(w, 409, err.Error())
		return
	}
	if err != nil {
		problem(w, 503, "Could not queue service refresh")
		return
	}
	a.audit(r, service+".refresh_queued", job.ID, map[string]any{"account_id": account, "region": region})
	write(w, 202, serviceRefreshResponse(job))
}
func serviceRefreshResponse(job syncer.Refresh) any {
	status, message := job.Status, job.Error
	if status == "partial" {
		status = "failed"
	}
	if len(job.FailedAccounts) > 0 {
		message = job.FailedAccounts[0].Message
	}
	base := ossRefreshJob{ID: job.ID, AccountID: job.AccountIDs[0], Status: status, CreatedAt: job.CreatedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt, DurationMS: job.DurationMS, Error: message}
	if job.Service == syncer.Desktops || job.Service == syncer.Users {
		region := job.Region
		if region == "" {
			region = "all"
		}
		return edsRefreshJob{ID: base.ID, AccountID: base.AccountID, Service: strings.TrimPrefix(job.Service, "eds."), Region: region, Status: base.Status, CreatedAt: base.CreatedAt, StartedAt: base.StartedAt, CompletedAt: base.CompletedAt, DurationMS: base.DurationMS, Error: base.Error}
	}
	return base
}
func (a *App) edsRefreshStatus(w http.ResponseWriter, r *http.Request) {
	a.serviceRefreshStatus(w, r, "eds")
}
func (a *App) ossRefreshStatus(w http.ResponseWriter, r *http.Request) {
	a.serviceRefreshStatus(w, r, syncer.Buckets)
}
func (a *App) securityGroupRefreshStatus(w http.ResponseWriter, r *http.Request) {
	a.serviceRefreshStatus(w, r, syncer.SecurityGroups)
}
func (a *App) serviceRefreshStatus(w http.ResponseWriter, r *http.Request, service string) {
	account, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	id := r.PathValue("jobID")
	if !validRefreshJobID(id) {
		problem(w, 400, "Invalid refresh job ID")
		return
	}
	u := current(r)
	// The receipt and every job are workspace-filtered before checking access.
	// Use the path account as the first filter; it cannot authorize another account.
	job, err := a.Sync.ServiceRefreshStatus(r.Context(), u.WorkspaceID, id, []int64{account}, service)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, syncer.ErrAccountAccess) {
		problem(w, 404, "Refresh job not found")
		return
	}
	if err != nil {
		dbError(w, err)
		return
	}
	if !a.accountAccess(r, account, "view") {
		problem(w, 403, "Cloud account access required")
		return
	}
	write(w, 200, serviceRefreshResponse(job))
}
