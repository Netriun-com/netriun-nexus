// SPDX-License-Identifier: AGPL-3.0-only

package entitlements

import (
	"context"
	"testing"
)

func TestCommunityProviderContract(t *testing.T) {
	service := New(CommunityProvider{Mode: DeploymentSelfHosted})
	snapshot := service.Snapshot(context.Background(), 42)
	if snapshot.DeploymentMode != DeploymentSelfHosted || snapshot.Edition != EditionCommunity || snapshot.Status != StatusCommunity {
		t.Fatalf("unexpected community snapshot: %+v", snapshot)
	}
	for _, feature := range CoreFeatures {
		if !snapshot.Features[feature] {
			t.Fatalf("Core feature %q must remain enabled in Community", feature)
		}
	}
	for _, feature := range EnterpriseFeatures {
		if snapshot.Features[feature] {
			t.Fatalf("Enterprise feature %q must be disabled in Community", feature)
		}
	}
	if snapshot.Limits[LimitHumanIdentities] != 6 || snapshot.Limits[LimitCloudAccounts] != 5 || snapshot.Limits[LimitAuditRetentionDays] != 30 {
		t.Fatalf("unexpected community limits: %+v", snapshot.Limits)
	}
}

func TestFeatureAndLimitDecisionsAreProviderDecisions(t *testing.T) {
	service := New(CommunityProvider{Mode: DeploymentCloud})
	ctx := context.Background()
	if denial := service.RequireFeature(ctx, 7, FeatureSSO); denial != nil {
		t.Fatalf("Core SSO must not require an Enterprise license: %v", denial)
	}
	if denial := service.RequireFeature(ctx, 7, FeatureAdvancedSSO); denial == nil || denial.Code != "feature_not_entitled" || denial.Feature != FeatureAdvancedSSO {
		t.Fatalf("unexpected feature decision: %+v", denial)
	}
	if denial := service.RequireLimit(ctx, 7, LimitCloudAccounts, 5); denial != nil {
		t.Fatalf("limit boundary should be allowed: %v", denial)
	}
	if denial := service.RequireLimit(ctx, 7, LimitCloudAccounts, 6); denial == nil || denial.Code != "limit_exceeded" || denial.Allowed != 5 || denial.Current != 6 {
		t.Fatalf("unexpected limit decision: %+v", denial)
	}
}

func TestDeploymentModeValidation(t *testing.T) {
	for _, value := range []string{"cloud", "self_hosted"} {
		if _, err := ParseDeploymentMode(value); err != nil {
			t.Fatalf("valid mode %q rejected: %v", value, err)
		}
	}
	if _, err := ParseDeploymentMode("desktop"); err == nil {
		t.Fatal("invalid deployment mode accepted")
	}
}
