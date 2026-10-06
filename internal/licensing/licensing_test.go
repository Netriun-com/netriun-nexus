// SPDX-License-Identifier: AGPL-3.0-only

package licensing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/netriun/nexus/internal/entitlements"
)

var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func testClaims() Claims {
	return Claims{
		LicenseID:      "lic_test_001",
		Customer:       Customer{OrganizationID: "org_test", OrganizationName: "Test Organization"},
		Product:        Product,
		Edition:        entitlements.EditionEnterprise,
		DeploymentMode: entitlements.DeploymentSelfHosted,
		Entitlements: Grant{
			Features: []entitlements.Feature{entitlements.FeatureAdvancedSSO, entitlements.FeatureCostManagement},
			Limits:   map[entitlements.Limit]int{entitlements.LimitCloudAccounts: 100},
		},
		IssuedAt:            fixedNow.Add(-time.Hour).Format(time.RFC3339),
		NotBefore:           fixedNow.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:           fixedNow.Add(24 * time.Hour).Format(time.RFC3339),
		InstallationBinding: InstallationBinding{Type: BindingInstallation, Value: "ins_test_001"},
		LicenseVersion:      LicenseVersion,
		KeyID:               "test-key-1",
	}
}

func testVerifier(t testing.TB, state KeyState) (Verifier, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Verifier{
		Keys:           Keyring{version: 1, keys: map[string]keyRecord{"test-key-1": {publicKey: publicKey, state: state}}},
		Now:            func() time.Time { return fixedNow },
		DeploymentMode: entitlements.DeploymentSelfHosted,
		InstallationID: "ins_test_001",
	}, privateKey
}

func signForTest(t *testing.T, claims Claims, privateKey ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsoncanonicalizer.Transform(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope{
		License:   payload,
		Signature: Signature{Algorithm: SignatureAlgorithm, Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))},
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestValidLicenseAndDeterministicJCS(t *testing.T) {
	verifier, privateKey := testVerifier(t, KeyActive)
	data := signForTest(t, testClaims(), privateKey)
	result := verifier.Verify(data)
	if result.Status != entitlements.StatusLicensed || result.Reason != "" || result.Claims == nil {
		t.Fatalf("valid license rejected: %+v", result)
	}
	provider := Provider{Community: entitlements.CommunityProvider{Mode: entitlements.DeploymentSelfHosted}, Evaluation: result}
	snapshot := provider.Snapshot(context.Background(), 1)
	if snapshot.Edition != entitlements.EditionEnterprise || !snapshot.Features[entitlements.FeatureAdvancedSSO] || !snapshot.Features[entitlements.FeatureSSO] || snapshot.Limits[entitlements.LimitCloudAccounts] != 100 {
		t.Fatalf("unexpected Enterprise snapshot: %+v", snapshot)
	}
}

func TestJCSGoldenVector(t *testing.T) {
	input := []byte(`{"z":0.002,"a":{"b":2,"a":1},"array":[3,{"d":4,"c":5}]}`)
	canonical, err := jsoncanonicalizer.Transform(input)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"a":{"a":1,"b":2},"array":[3,{"c":5,"d":4}],"z":0.002}`
	if string(canonical) != expected {
		t.Fatalf("JCS mismatch:\nwant %s\n got %s", expected, canonical)
	}
}

func TestLicenseSecurityMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Claims, *Verifier)
		state  KeyState
		status entitlements.Status
		reason string
	}{
		{name: "expired", mutate: func(c *Claims, _ *Verifier) {
			c.IssuedAt, c.NotBefore, c.ExpiresAt = fixedNow.Add(-60*24*time.Hour).Format(time.RFC3339), fixedNow.Add(-60*24*time.Hour).Format(time.RFC3339), fixedNow.Add(-15*24*time.Hour).Format(time.RFC3339)
		}, state: KeyActive, status: entitlements.StatusExpired, reason: "license_expired"},
		{name: "grace period", mutate: func(c *Claims, _ *Verifier) {
			c.IssuedAt, c.NotBefore, c.ExpiresAt = fixedNow.Add(-30*24*time.Hour).Format(time.RFC3339), fixedNow.Add(-30*24*time.Hour).Format(time.RFC3339), fixedNow.Add(-7*24*time.Hour).Format(time.RFC3339)
		}, state: KeyActive, status: entitlements.StatusGrace, reason: "license_grace_period"},
		{name: "not yet valid", mutate: func(c *Claims, _ *Verifier) { c.NotBefore = fixedNow.Add(6 * time.Minute).Format(time.RFC3339) }, state: KeyActive, status: entitlements.StatusInvalid, reason: "not_yet_valid"},
		{name: "clock skew accepted", mutate: func(c *Claims, _ *Verifier) { c.NotBefore = fixedNow.Add(4 * time.Minute).Format(time.RFC3339) }, state: KeyActive, status: entitlements.StatusLicensed},
		{name: "wrong product", mutate: func(c *Claims, _ *Verifier) { c.Product = "another-product" }, state: KeyActive, status: entitlements.StatusInvalid, reason: "wrong_product"},
		{name: "wrong edition", mutate: func(c *Claims, _ *Verifier) { c.Edition = entitlements.EditionCommunity }, state: KeyActive, status: entitlements.StatusInvalid, reason: "wrong_edition"},
		{name: "wrong deployment mode", mutate: func(c *Claims, _ *Verifier) { c.DeploymentMode = entitlements.DeploymentCloud }, state: KeyActive, status: entitlements.StatusInvalid, reason: "wrong_deployment_mode"},
		{name: "invalid binding", mutate: func(c *Claims, _ *Verifier) { c.InstallationBinding.Value = "ins_clone" }, state: KeyActive, status: entitlements.StatusInvalid, reason: "invalid_installation_binding"},
		{name: "missing required field", mutate: func(c *Claims, _ *Verifier) { c.LicenseID = "" }, state: KeyActive, status: entitlements.StatusInvalid, reason: "missing_required_fields"},
		{name: "unknown feature", mutate: func(c *Claims, _ *Verifier) { c.Entitlements.Features = append(c.Entitlements.Features, "future_typo") }, state: KeyActive, status: entitlements.StatusInvalid, reason: "invalid_entitlements"},
		{name: "retired key verifies", mutate: func(_ *Claims, _ *Verifier) {}, state: KeyRetired, status: entitlements.StatusLicensed},
		{name: "revoked key", mutate: func(_ *Claims, _ *Verifier) {}, state: KeyRevoked, status: entitlements.StatusInvalid, reason: "revoked_key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, privateKey := testVerifier(t, test.state)
			claims := testClaims()
			test.mutate(&claims, &verifier)
			result := verifier.Verify(signForTest(t, claims, privateKey))
			if result.Status != test.status || result.Reason != test.reason {
				t.Fatalf("got status=%q reason=%q", result.Status, result.Reason)
			}
		})
	}
}

func TestSignatureFailuresAndUnknownKey(t *testing.T) {
	verifier, privateKey := testVerifier(t, KeyActive)
	valid := signForTest(t, testClaims(), privateKey)

	var modified map[string]any
	if err := json.Unmarshal(valid, &modified); err != nil {
		t.Fatal(err)
	}
	modified["license"].(map[string]any)["product"] = "modified"
	modifiedBytes, _ := json.Marshal(modified)
	if result := verifier.Verify(modifiedBytes); result.Reason != "invalid_signature" {
		t.Fatalf("modified payload was not rejected before semantics: %+v", result)
	}

	var envelope Envelope
	_ = json.Unmarshal(valid, &envelope)
	envelope.Signature.Value = strings.Repeat("A", 86)
	invalidSignature, _ := json.Marshal(envelope)
	if result := verifier.Verify(invalidSignature); result.Reason != "invalid_signature" {
		t.Fatalf("invalid signature accepted: %+v", result)
	}

	claims := testClaims()
	claims.KeyID = "unknown-key"
	if result := verifier.Verify(signForTest(t, claims, privateKey)); result.Reason != "unknown_key_id" {
		t.Fatalf("unknown key was not rejected: %+v", result)
	}
}

func TestMalformedInputsFailSafeAndCommunityFallback(t *testing.T) {
	verifier, privateKey := testVerifier(t, KeyActive)
	inputs := [][]byte{
		nil,
		[]byte(`{`),
		[]byte(`{"license":{},"license":{},"signature":{}}`),
		[]byte(`{"license":{},"signature":{},"extra":true}`),
		make([]byte, MaxLicenseBytes+1),
	}
	for _, input := range inputs {
		if result := verifier.Verify(input); result.Status != entitlements.StatusInvalid {
			t.Fatalf("malformed license accepted: %+v", result)
		}
	}
	valid := signForTest(t, testClaims(), privateKey)
	var envelope map[string]any
	_ = json.Unmarshal(valid, &envelope)
	license := envelope["license"].(map[string]any)
	license["license_id_duplicate_test"] = "x"
	unknown, _ := json.Marshal(envelope)
	if result := verifier.Verify(unknown); result.Status != entitlements.StatusInvalid {
		t.Fatalf("unknown field accepted: %+v", result)
	}

	provider := Provider{
		Community:  entitlements.CommunityProvider{Mode: entitlements.DeploymentSelfHosted},
		Evaluation: Evaluation{Status: entitlements.StatusInvalid, Reason: "invalid_signature"},
	}
	snapshot := provider.Snapshot(context.Background(), 1)
	if snapshot.Edition != entitlements.EditionCommunity || !snapshot.Features[entitlements.FeatureSSO] || snapshot.Features[entitlements.FeatureAdvancedSSO] {
		t.Fatalf("invalid license did not fall back safely: %+v", snapshot)
	}
}

func TestKeyringValidation(t *testing.T) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	encoded := base64.RawURLEncoding.EncodeToString(publicKey)
	data := []byte(`{"version":1,"keys":[{"key_id":"old","algorithm":"Ed25519","public_key":"` + encoded + `","state":"retired"}]}`)
	ring, err := ParseKeyring(data)
	if err != nil || ring.keys["old"].state != KeyRetired {
		t.Fatalf("valid keyring rejected: ring=%+v err=%v", ring, err)
	}
	if _, err = ParseKeyring([]byte(`{"version":1,"keys":[{"key_id":"x","algorithm":"Ed25519","public_key":"bad","state":"active"}]}`)); err == nil {
		t.Fatal("invalid public key accepted")
	}
}

func TestInstallationBindingRestoreCloneAndReissue(t *testing.T) {
	verifier, privateKey := testVerifier(t, KeyActive)
	license := signForTest(t, testClaims(), privateKey)
	if result := verifier.Verify(license); result.Status != entitlements.StatusLicensed {
		t.Fatalf("original installation rejected: %+v", result)
	}
	// A database backup/restore and a byte-for-byte clone both preserve the
	// installation identity. A clone is therefore the same licensed identity,
	// not an automatic license bypass or a newly licensed installation.
	restored := verifier
	restored.InstallationID = "ins_test_001"
	if result := restored.Verify(license); result.Status != entitlements.StatusLicensed {
		t.Fatalf("restored installation rejected: %+v", result)
	}
	// A fresh database creates a different ID and requires reissuance.
	fresh := verifier
	fresh.InstallationID = "ins_new_installation"
	if result := fresh.Verify(license); result.Reason != "invalid_installation_binding" {
		t.Fatalf("fresh installation accepted an old license: %+v", result)
	}
}

func FuzzVerifierNeverPanics(f *testing.F) {
	verifier, _ := testVerifier(f, KeyActive)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"license":null,"signature":null}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_ = verifier.Verify(input)
	})
}
