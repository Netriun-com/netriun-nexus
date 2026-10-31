// SPDX-License-Identifier: AGPL-3.0-only

package commercialplatform_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/netriun/nexus/internal/entitlements"
	"github.com/netriun/nexus/internal/entitlements/commercialplatform"
)

func TestSnapshotMatchesCommunity(t *testing.T) {
	for _, mode := range []entitlements.DeploymentMode{entitlements.DeploymentSelfHosted, entitlements.DeploymentCloud} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			const workspaceID int64 = 42
			got := (commercialplatform.Provider{Mode: mode}).Snapshot(ctx, workspaceID)
			want := (entitlements.CommunityProvider{Mode: mode}).Snapshot(ctx, workspaceID)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot differs from Community: got %+v, want %+v", got, want)
			}
		})
	}
}
