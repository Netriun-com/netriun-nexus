// SPDX-License-Identifier: AGPL-3.0-only

package licensing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netriun/nexus/internal/entitlements"
)

// TestExternalLicenseV1Interop is an opt-in black-box conformance hook for a
// separately implemented issuer. The external test generates an ephemeral key
// and artifact; no private or persistent test key enters this repository.
func TestExternalLicenseV1Interop(t *testing.T) {
	directory := os.Getenv("NEXUS_INTEROP_DIR")
	if directory == "" {
		t.Skip("NEXUS_INTEROP_DIR is not set")
	}
	artifact, err := os.ReadFile(filepath.Join(directory, "license.json"))
	if err != nil {
		t.Fatal(err)
	}
	metadataBytes, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		PublicKey      string `json:"public_key"`
		KeyID          string `json:"key_id"`
		InstallationID string `json:"installation_id"`
		Now            string `json:"now"`
	}
	if err = json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	keyringJSON := fmt.Sprintf(`{"version":1,"keys":[{"key_id":%q,"algorithm":"Ed25519","public_key":%q,"state":"active","created_at":"2026-01-01T00:00:00Z","usage":"license_signing"}]}`, metadata.KeyID, metadata.PublicKey)
	keyring, err := ParseKeyring([]byte(keyringJSON))
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, metadata.Now)
	if err != nil {
		t.Fatal(err)
	}
	result := (Verifier{Keys: keyring, Now: func() time.Time { return now }, Product: Product, DeploymentMode: entitlements.DeploymentSelfHosted, InstallationID: metadata.InstallationID}).Verify(artifact)
	if result.Status != entitlements.StatusLicensed {
		t.Fatalf("external license-v1 artifact rejected: status=%s reason=%s", result.Status, result.Reason)
	}
}
