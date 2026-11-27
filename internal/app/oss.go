// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/netriun/nexus/internal/cloud"
)

var ossBucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

type ossRefreshJob struct {
	ID            string     `json:"id"`
	WorkspaceID   int64      `json:"-"`
	RequestedBy   int64      `json:"-"`
	RequestedName string     `json:"-"`
	AccountID     int64      `json:"account_id"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func (a *App) ossAccount(w http.ResponseWriter, r *http.Request, mode string) (int64, bool) {
	accountID, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}
	if !a.accountAccess(r, accountID, mode) {
		problem(w, http.StatusForbidden, "Cloud account access required")
		return 0, false
	}
	var provider string
	if err := a.DB.QueryRow(r.Context(), `SELECT provider FROM cloud_accounts WHERE id=$1 AND workspace_id=$2`, accountID, current(r).WorkspaceID).Scan(&provider); err != nil {
		dbError(w, err)
		return 0, false
	}
	if provider != "alibaba" {
		problem(w, http.StatusBadRequest, "The selected account is not an Alibaba Cloud connection")
		return 0, false
	}
	return accountID, true
}

func (a *App) ossBuckets(w http.ResponseWriter, r *http.Request) {
	accountID, ok := a.ossAccount(w, r, "view")
	if !ok {
		return
	}
	payload, _, err := a.serviceSnapshot(r.Context(), current(r).WorkspaceID, accountID, "oss.buckets", "")
	if err != nil {
		dbError(w, err)
		return
	}
	write(w, http.StatusOK, payload)
}

func (a *App) collectOSSBuckets(ctx context.Context, workspaceID, accountID int64) error {
	return a.collectService(ctx, workspaceID, accountID, "oss.buckets", "*")
}

func (a *App) createOSSBucket(w http.ResponseWriter, r *http.Request) {
	accountID, ok := a.ossAccount(w, r, "manage")
	if !ok {
		return
	}
	var in struct {
		Name           string `json:"name"`
		Region         string `json:"region"`
		StorageClass   string `json:"storage_class"`
		RedundancyType string `json:"redundancy_type"`
		ConfirmCost    bool   `json:"confirm_cost"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name, in.Region = strings.TrimSpace(in.Name), strings.TrimSpace(in.Region)
	if !ossBucketNamePattern.MatchString(in.Name) {
		problem(w, http.StatusBadRequest, "Bucket name must be 3–63 lowercase letters, numbers, or hyphens and must start and end with a letter or number")
		return
	}
	if !regionPattern.MatchString(in.Region) {
		problem(w, http.StatusBadRequest, "A valid Alibaba Cloud region is required")
		return
	}
	if in.StorageClass != "Standard" && in.StorageClass != "IA" && in.StorageClass != "Archive" && in.StorageClass != "ColdArchive" {
		problem(w, http.StatusBadRequest, "Unsupported OSS storage class")
		return
	}
	if in.RedundancyType != "LRS" && in.RedundancyType != "ZRS" {
		problem(w, http.StatusBadRequest, "OSS redundancy must be LRS or ZRS")
		return
	}
	if !in.ConfirmCost {
		problem(w, http.StatusBadRequest, "Explicit cost confirmation is required")
		return
	}
	u := current(r)
	if err := a.record(r.Context(), u.WorkspaceID, u.ID, u.Username, "oss.bucket.create.requested", in.Name, map[string]any{"account_id": accountID, "region": in.Region, "storage_class": in.StorageClass, "redundancy_type": in.RedundancyType, "acl": "private"}); err != nil {
		problem(w, http.StatusServiceUnavailable, "Bucket was not submitted because audit storage is unavailable")
		return
	}
	credentials, err := a.credentials(r.Context(), u.WorkspaceID, accountID, "alibaba")
	if err != nil {
		problem(w, http.StatusBadRequest, "The selected account is not an Alibaba Cloud connection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	err = cloud.OSSCreateBucket(ctx, cloud.OSSClient(in.Region, credentials.AccessKey, credentials.SecretKey, credentials.SessionToken), cloud.OSSCreateBucketInput{Name: in.Name, Region: in.Region, StorageClass: in.StorageClass, RedundancyType: in.RedundancyType})
	if err != nil {
		a.audit(r, "oss.bucket.create.failed", in.Name, nil)
		slog.Warn("Alibaba OSS bucket creation failed", "account_id", accountID, "bucket", in.Name, "region", in.Region, "error", err)
		problem(w, http.StatusBadGateway, "Alibaba OSS rejected bucket creation; check global name availability, region support, RAM permissions, and account activation")
		return
	}
	a.audit(r, "oss.bucket.create.accepted", in.Name, nil)
	write(w, http.StatusAccepted, map[string]string{"status": "created", "name": in.Name, "region": in.Region})
}
