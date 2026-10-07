// SPDX-License-Identifier: AGPL-3.0-only

package entitlements

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPublishedManifestMatchesCoreVocabulary(t *testing.T) {
	data, err := os.ReadFile("../../api/entitlements/v1/netriun-nexus.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Product              string `json:"product"`
		LicenseSchemaVersion int    `json:"license_schema_version"`
		Features             []struct {
			Key string `json:"key"`
		} `json:"features"`
		Limits []struct {
			Key string `json:"key"`
		} `json:"limits"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Product != "netriun-nexus" || manifest.LicenseSchemaVersion != 1 {
		t.Fatalf("unexpected product manifest identity: %+v", manifest)
	}
	features := map[Feature]bool{}
	for _, definition := range manifest.Features {
		features[Feature(definition.Key)] = true
	}
	for _, expected := range EnterpriseFeatures {
		if !features[expected] {
			t.Fatalf("enterprise feature %q is missing from the published manifest", expected)
		}
		delete(features, expected)
	}
	if len(features) != 0 {
		t.Fatalf("published manifest contains unknown enterprise features: %v", features)
	}
	limits := map[Limit]bool{}
	for _, definition := range manifest.Limits {
		limits[Limit(definition.Key)] = true
	}
	for _, expected := range KnownLimits {
		if !limits[expected] {
			t.Fatalf("limit %q is missing from the published manifest", expected)
		}
		delete(limits, expected)
	}
	if len(limits) != 0 {
		t.Fatalf("published manifest contains unknown limits: %v", limits)
	}
}
