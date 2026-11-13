// SPDX-License-Identifier: AGPL-3.0-only

package sync_test

import (
	"testing"
	"time"

	syncer "github.com/netriun/nexus/internal/sync"
)

func TestSyncConfiguration(t *testing.T) {
	for _, name := range []string{"NEXUS_SYNC_COMPUTE_INTERVAL", "NEXUS_SYNC_DESKTOPS_INTERVAL", "NEXUS_SYNC_USERS_INTERVAL", "NEXUS_SYNC_SECURITY_GROUPS_INTERVAL", "NEXUS_SYNC_BUCKETS_INTERVAL", "NEXUS_SYNC_EMPTY_INTERVAL", "NEXUS_SYNC_TICK_INTERVAL", "NEXUS_SYNC_RECONCILE_INTERVAL", "NEXUS_JOB_RETENTION"} {
		t.Setenv(name, "")
	}
	c, err := syncer.LoadConfig()
	if err != nil || c != syncer.DefaultConfig() {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	t.Setenv("NEXUS_SYNC_COMPUTE_INTERVAL", "2m")
	t.Setenv("NEXUS_JOB_RETENTION", "24h")
	c, err = syncer.LoadConfig()
	if err != nil || c.Compute != 2*time.Minute || c.Retention != 24*time.Hour {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	for _, bad := range []string{"0s", "-1m", "500ms", "seven days"} {
		t.Setenv("NEXUS_JOB_RETENTION", bad)
		if _, err := syncer.LoadConfig(); err == nil {
			t.Fatalf("accepted retention %q", bad)
		}
	}
}
