// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/netriun/nexus/internal/licensing"
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

func (a *App) advanceLicenseTimeFloor(ctx context.Context) (time.Time, error) {
	var floor time.Time
	err := a.DB.QueryRow(ctx, `
UPDATE installation_identity
SET license_time_floor=GREATEST(license_time_floor,clock_timestamp())
WHERE singleton=true
RETURNING license_time_floor`).Scan(&floor)
	return floor.UTC(), err
}

func (a *App) persistLicenseTimeFloor(ctx context.Context, floor time.Time) error {
	_, err := a.DB.Exec(ctx, `
UPDATE installation_identity
SET license_time_floor=GREATEST(license_time_floor,$1)
WHERE singleton=true`, floor.UTC())
	return err
}

func (a *App) startLicenseClockPersistence() {
	if a.licenseClock == nil {
		return
	}
	a.backgroundWG.Add(1)
	go func() {
		defer a.backgroundWG.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-a.backgroundCtx.Done():
				return
			case observed := <-ticker.C:
				floor, _ := a.licenseClock.Observe(observed, licensing.DefaultClockSkew)
				ctx, cancel := context.WithTimeout(a.backgroundCtx, 5*time.Second)
				if err := a.persistLicenseTimeFloor(ctx, floor); err != nil && ctx.Err() == nil {
					slog.Error("license time floor persistence failed", "error", err)
				}
				cancel()
			}
		}
	}()
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
