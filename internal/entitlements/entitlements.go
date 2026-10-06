// SPDX-License-Identifier: AGPL-3.0-only

package entitlements

import (
	"context"
	"fmt"
)

type Feature string

const (
	// Core capabilities are published under AGPL-3.0-only and remain enabled in
	// Community. Keep these keys for API compatibility; they are not Enterprise
	// license gates.
	FeatureSSO               Feature = "sso"
	FeatureCustomAccessRoles Feature = "custom_access_roles"
	FeatureBillingReports    Feature = "billing_reports"
	FeatureBillingExport     Feature = "billing_export"

	// Enterprise capabilities are implemented out of process and are the only
	// capabilities that an Enterprise license may enable.
	FeatureAdvancedSSO         Feature = "advanced_sso"
	FeatureIdentityGovernance  Feature = "identity_governance"
	FeatureCostManagement      Feature = "cost_management"
	FeatureCustomReportBuilder Feature = "custom_report_builder"
	FeatureScheduledReports    Feature = "scheduled_reports"
	FeaturePolicyAutomation    Feature = "policy_automation"
	FeatureHAOperations        Feature = "ha_operations"
)

var CoreFeatures = []Feature{
	FeatureSSO,
	FeatureCustomAccessRoles,
	FeatureBillingReports,
	FeatureBillingExport,
}

var EnterpriseFeatures = []Feature{
	FeatureAdvancedSSO,
	FeatureIdentityGovernance,
	FeatureCostManagement,
	FeatureCustomReportBuilder,
	FeatureScheduledReports,
	FeaturePolicyAutomation,
	FeatureHAOperations,
}

var KnownFeatures = append(append([]Feature{}, CoreFeatures...), EnterpriseFeatures...)

type Limit string

const (
	LimitWorkspaces         Limit = "workspaces"
	LimitHumanIdentities    Limit = "human_identities"
	LimitCloudAccounts      Limit = "cloud_accounts"
	LimitAuditRetentionDays Limit = "audit_retention_days"
)

var KnownLimits = []Limit{
	LimitWorkspaces,
	LimitHumanIdentities,
	LimitCloudAccounts,
	LimitAuditRetentionDays,
}

type Edition string

const (
	EditionCommunity  Edition = "community"
	EditionEnterprise Edition = "enterprise"
)

type Status string

const (
	StatusCommunity Status = "community"
	StatusLicensed  Status = "licensed"
	StatusGrace     Status = "grace"
	StatusExpired   Status = "expired"
	StatusInvalid   Status = "invalid"
)

type DeploymentMode string

const (
	DeploymentCloud      DeploymentMode = "cloud"
	DeploymentSelfHosted DeploymentMode = "self_hosted"
)

func ParseDeploymentMode(value string) (DeploymentMode, error) {
	mode := DeploymentMode(value)
	if mode != DeploymentCloud && mode != DeploymentSelfHosted {
		return "", fmt.Errorf("DEPLOYMENT_MODE must be cloud or self_hosted")
	}
	return mode, nil
}

type Snapshot struct {
	DeploymentMode DeploymentMode   `json:"deployment_mode"`
	Edition        Edition          `json:"edition"`
	Status         Status           `json:"status"`
	Features       map[Feature]bool `json:"features"`
	Limits         map[Limit]int    `json:"limits"`
	License        *LicenseMetadata `json:"license,omitempty"`
}

type LicenseMetadata struct {
	LicenseID string `json:"license_id,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Provider interface {
	Snapshot(context.Context, int64) Snapshot
}

type CommunityProvider struct {
	Mode DeploymentMode
}

func (p CommunityProvider) Snapshot(_ context.Context, _ int64) Snapshot {
	features := make(map[Feature]bool, len(KnownFeatures))
	for _, feature := range KnownFeatures {
		features[feature] = false
	}
	for _, feature := range CoreFeatures {
		features[feature] = true
	}
	return Snapshot{
		DeploymentMode: p.Mode,
		Edition:        EditionCommunity,
		Status:         StatusCommunity,
		Features:       features,
		Limits: map[Limit]int{
			LimitWorkspaces:         1,
			LimitHumanIdentities:    6,
			LimitCloudAccounts:      5,
			LimitAuditRetentionDays: 30,
		},
	}
}

type DecisionError struct {
	Code    string  `json:"code"`
	Message string  `json:"error"`
	Feature Feature `json:"feature,omitempty"`
	Limit   Limit   `json:"limit,omitempty"`
	Current int     `json:"current,omitempty"`
	Allowed int     `json:"allowed,omitempty"`
}

func (e *DecisionError) Error() string { return e.Message }

type Service struct {
	provider Provider
}

func New(provider Provider) *Service {
	return &Service{provider: provider}
}

func (s *Service) Snapshot(ctx context.Context, workspaceID int64) Snapshot {
	return s.provider.Snapshot(ctx, workspaceID)
}

func (s *Service) RequireFeature(ctx context.Context, workspaceID int64, feature Feature) *DecisionError {
	snapshot := s.Snapshot(ctx, workspaceID)
	if snapshot.Features[feature] {
		return nil
	}
	return &DecisionError{
		Code:    "feature_not_entitled",
		Message: "This capability is not included in the current edition",
		Feature: feature,
	}
}

func (s *Service) RequireLimit(ctx context.Context, workspaceID int64, limit Limit, requested int) *DecisionError {
	snapshot := s.Snapshot(ctx, workspaceID)
	allowed, exists := snapshot.Limits[limit]
	if exists && requested <= allowed {
		return nil
	}
	return &DecisionError{
		Code:    "limit_exceeded",
		Message: "This change exceeds the current edition limit",
		Limit:   limit,
		Current: requested,
		Allowed: allowed,
	}
}
