// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

func (a *App) ensureInstallationID(ctx context.Context) (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	candidate := "ins_" + base64.RawURLEncoding.EncodeToString(random)
	var installationID string
	err := a.DB.QueryRow(ctx, `
INSERT INTO installation_identity(singleton,installation_id)
VALUES(true,$1)
ON CONFLICT(singleton) DO UPDATE SET singleton=EXCLUDED.singleton
RETURNING installation_id`, candidate).Scan(&installationID)
	return installationID, err
}

func (a *App) installationStatus(w http.ResponseWriter, r *http.Request) {
	if !admin(w, r) {
		return
	}
	snapshot := a.Entitlements.Snapshot(r.Context(), current(r).WorkspaceID)
	write(w, http.StatusOK, map[string]any{
		"installation_id":               a.InstallationID,
		"deployment_mode":               snapshot.DeploymentMode,
		"edition":                       snapshot.Edition,
		"license_status":                snapshot.Status,
		"license":                       snapshot.License,
		"enterprise_service_configured": a.Enterprise != nil,
	})
}
