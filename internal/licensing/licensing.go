// SPDX-License-Identifier: AGPL-3.0-only

// Package licensing verifies offline Enterprise licenses. It contains no
// signing key and never makes Community operation depend on a license.
package licensing

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/netriun/nexus/internal/entitlements"
)

const (
	Product             = "netriun-nexus"
	LicenseVersion      = 1
	MaxLicenseBytes     = 64 << 10
	DefaultClockSkew    = 5 * time.Minute
	DefaultGracePeriod  = 14 * 24 * time.Hour
	SignatureAlgorithm  = "Ed25519"
	BindingInstallation = "installation_id"
	KeyUsageSigning     = "license_signing"
)

type Customer struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

type InstallationBinding struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Grant struct {
	Features []entitlements.Feature     `json:"features"`
	Limits   map[entitlements.Limit]int `json:"limits,omitempty"`
}

type Claims struct {
	LicenseID           string                      `json:"license_id"`
	Customer            Customer                    `json:"customer"`
	Product             string                      `json:"product"`
	Edition             entitlements.Edition        `json:"edition"`
	DeploymentMode      entitlements.DeploymentMode `json:"deployment_mode"`
	Entitlements        Grant                       `json:"entitlements"`
	IssuedAt            string                      `json:"issued_at"`
	NotBefore           string                      `json:"not_before"`
	ExpiresAt           string                      `json:"expires_at"`
	InstallationBinding InstallationBinding         `json:"installation_binding"`
	LicenseVersion      int                         `json:"license_version"`
	KeyID               string                      `json:"key_id"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type Envelope struct {
	License   json.RawMessage `json:"license"`
	Signature Signature       `json:"signature"`
}

type KeyState string

const (
	KeyActive  KeyState = "active"
	KeyRetired KeyState = "retired"
	KeyRevoked KeyState = "revoked"
)

type TrustedKey struct {
	ID        string   `json:"key_id"`
	Algorithm string   `json:"algorithm"`
	PublicKey string   `json:"public_key"`
	State     KeyState `json:"state"`
	CreatedAt string   `json:"created_at"`
	Usage     string   `json:"usage"`
}

type keyringDocument struct {
	Version int          `json:"version"`
	Keys    []TrustedKey `json:"keys"`
}

type keyRecord struct {
	publicKey ed25519.PublicKey
	state     KeyState
}

type Keyring struct {
	version int
	keys    map[string]keyRecord
}

//go:embed trust/keyring.json
var trustFS embed.FS

func DefaultKeyring() (Keyring, error) {
	data, err := trustFS.ReadFile("trust/keyring.json")
	if err != nil {
		return Keyring{}, err
	}
	return ParseKeyring(data)
}

func ParseKeyring(data []byte) (Keyring, error) {
	var document keyringDocument
	if err := strictDecode(data, &document); err != nil {
		return Keyring{}, fmt.Errorf("invalid public keyring: %w", err)
	}
	if document.Version != 1 {
		return Keyring{}, errors.New("unsupported public keyring version")
	}
	ring := Keyring{version: document.Version, keys: make(map[string]keyRecord, len(document.Keys))}
	for _, key := range document.Keys {
		_, createdAtErr := parseTimestamp(key.CreatedAt)
		if !validIdentifier(key.ID) || key.Algorithm != SignatureAlgorithm || key.Usage != KeyUsageSigning || createdAtErr != nil || (key.State != KeyActive && key.State != KeyRetired && key.State != KeyRevoked) {
			return Keyring{}, errors.New("invalid public key metadata")
		}
		if _, exists := ring.keys[key.ID]; exists {
			return Keyring{}, errors.New("duplicate public key ID")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(key.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return Keyring{}, errors.New("invalid Ed25519 public key")
		}
		ring.keys[key.ID] = keyRecord{publicKey: ed25519.PublicKey(decoded), state: key.State}
	}
	return ring, nil
}

type Evaluation struct {
	Status entitlements.Status
	Claims *Claims
	Reason string
}

type Verifier struct {
	Keys           Keyring
	Now            func() time.Time
	ClockSkew      time.Duration
	GracePeriod    time.Duration
	Product        string
	DeploymentMode entitlements.DeploymentMode
	InstallationID string
	TimeGuard      *TimeGuard
}

// TimeGuard prevents a running process from moving its license clock
// backwards. Its initial floor is loaded from persistent installation state.
// It is not a defense against an administrator rolling back both the database
// and the host clock; that remains an operational and contractual boundary.
type TimeGuard struct {
	mu        sync.Mutex
	highWater time.Time
}

func NewTimeGuard(initial time.Time) *TimeGuard {
	return &TimeGuard{highWater: initial.UTC()}
}

func (g *TimeGuard) Observe(now time.Time, skew time.Duration) (time.Time, bool) {
	if g == nil {
		return now.UTC(), false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now = now.UTC()
	if !g.highWater.IsZero() && now.Add(skew).Before(g.highWater) {
		return g.highWater, true
	}
	if now.After(g.highWater) {
		g.highWater = now
	}
	return g.highWater, false
}

func (g *TimeGuard) HighWater() time.Time {
	if g == nil {
		return time.Time{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.highWater
}

func (v Verifier) Verify(data []byte) (result Evaluation) {
	result = Evaluation{Status: entitlements.StatusInvalid, Reason: "malformed_license"}
	defer func() {
		if recover() != nil {
			result = Evaluation{Status: entitlements.StatusInvalid, Reason: "malformed_license"}
		}
	}()
	if len(data) == 0 || len(data) > MaxLicenseBytes {
		return result
	}
	if !utf8.Valid(data) || rejectDuplicateKeys(data) != nil {
		return result
	}
	if err := validateEnvelopeShape(data); err != nil {
		return result
	}
	var envelope Envelope
	if err := strictDecode(data, &envelope); err != nil || len(envelope.License) == 0 {
		return result
	}
	var claims Claims
	if err := strictDecode(envelope.License, &claims); err != nil {
		return result
	}
	if envelope.Signature.Algorithm != SignatureAlgorithm {
		result.Reason = "unsupported_signature_algorithm"
		return result
	}
	record, ok := v.Keys.keys[claims.KeyID]
	if !ok {
		result.Reason = "unknown_key_id"
		return result
	}
	if record.state == KeyRevoked {
		result.Reason = "revoked_key"
		return result
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		result.Reason = "invalid_signature"
		return result
	}
	canonical, err := jsoncanonicalizer.Transform(envelope.License)
	if err != nil || !ed25519.Verify(record.publicKey, canonical, signature) {
		result.Reason = "invalid_signature"
		return result
	}
	if reason := validateClaims(claims, v); reason != "" {
		result.Reason = reason
		return result
	}
	now, rollback := v.TimeGuard.Observe(v.now(), v.skew())
	if rollback {
		result.Reason = "clock_rollback_detected"
		return result
	}
	issuedAt, _ := parseTimestamp(claims.IssuedAt)
	notBefore, _ := parseTimestamp(claims.NotBefore)
	expiresAt, _ := parseTimestamp(claims.ExpiresAt)
	if issuedAt.After(now.Add(v.skew())) || notBefore.After(now.Add(v.skew())) {
		result.Reason = "not_yet_valid"
		return result
	}
	result.Claims = &claims
	if now.After(expiresAt.Add(v.skew())) {
		if now.After(expiresAt.Add(v.grace()).Add(v.skew())) {
			result.Status = entitlements.StatusExpired
			result.Reason = "license_expired"
			return result
		}
		result.Status = entitlements.StatusGrace
		result.Reason = "license_grace_period"
		return result
	}
	result.Status = entitlements.StatusLicensed
	result.Reason = ""
	return result
}

func (v Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v Verifier) skew() time.Duration {
	if v.ClockSkew > 0 {
		return v.ClockSkew
	}
	return DefaultClockSkew
}

func (v Verifier) grace() time.Duration {
	if v.GracePeriod > 0 {
		return v.GracePeriod
	}
	return DefaultGracePeriod
}

func validateClaims(c Claims, v Verifier) string {
	if c.LicenseVersion != LicenseVersion {
		return "unsupported_license_version"
	}
	if !validIdentifier(c.LicenseID) || !validIdentifier(c.KeyID) || strings.TrimSpace(c.Customer.OrganizationID) == "" || strings.TrimSpace(c.Customer.OrganizationName) == "" {
		return "missing_required_fields"
	}
	product := v.Product
	if product == "" {
		product = Product
	}
	if c.Product != product {
		return "wrong_product"
	}
	if c.Edition != entitlements.EditionEnterprise {
		return "wrong_edition"
	}
	if c.DeploymentMode != v.DeploymentMode {
		return "wrong_deployment_mode"
	}
	if c.InstallationBinding.Type != BindingInstallation || v.InstallationID == "" || subtle.ConstantTimeCompare([]byte(c.InstallationBinding.Value), []byte(v.InstallationID)) != 1 {
		return "invalid_installation_binding"
	}
	issuedAt, errIssued := parseTimestamp(c.IssuedAt)
	notBefore, errNotBefore := parseTimestamp(c.NotBefore)
	expiresAt, errExpires := parseTimestamp(c.ExpiresAt)
	if errIssued != nil || errNotBefore != nil || errExpires != nil || !expiresAt.After(issuedAt) || !expiresAt.After(notBefore) {
		return "invalid_time_range"
	}
	allowedFeatures := make(map[entitlements.Feature]bool, len(entitlements.EnterpriseFeatures))
	for _, feature := range entitlements.EnterpriseFeatures {
		allowedFeatures[feature] = true
	}
	seen := make(map[entitlements.Feature]bool, len(c.Entitlements.Features))
	for _, feature := range c.Entitlements.Features {
		if !allowedFeatures[feature] || seen[feature] {
			return "invalid_entitlements"
		}
		seen[feature] = true
	}
	allowedLimits := make(map[entitlements.Limit]bool, len(entitlements.KnownLimits))
	for _, limit := range entitlements.KnownLimits {
		allowedLimits[limit] = true
	}
	for limit, value := range c.Entitlements.Limits {
		if !allowedLimits[limit] || value < 0 {
			return "invalid_entitlements"
		}
	}
	return ""
}

func parseTimestamp(value string) (time.Time, error) {
	if value == "" || strings.Contains(value, ".") || !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("timestamp must be UTC RFC3339 seconds")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Format(time.RFC3339) != value {
		return time.Time{}, errors.New("timestamp must be canonical UTC RFC3339 seconds")
	}
	return parsed, nil
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func strictDecode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON must be valid UTF-8")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	var walk func(json.Token, int) error
	walk = func(token json.Token, depth int) error {
		if depth > 64 {
			return errors.New("JSON nesting exceeds limit")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := nameToken.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid JSON object member")
				}
				seen[name] = true
				valueToken, err := decoder.Token()
				if err != nil {
					return err
				}
				if err = walk(valueToken, depth+1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				valueToken, err := decoder.Token()
				if err != nil {
					return err
				}
				if err = walk(valueToken, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		closing, err := decoder.Token()
		if err != nil || closing != matchingDelimiter(delimiter) {
			return errors.New("invalid JSON structure")
		}
		return nil
	}
	if err = walk(first, 0); err != nil {
		return err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func validateEnvelopeShape(data []byte) error {
	var envelopeFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelopeFields); err != nil {
		return err
	}
	if err := requireFields(envelopeFields, "license", "signature"); err != nil {
		return err
	}
	var licenseFields map[string]json.RawMessage
	if err := json.Unmarshal(envelopeFields["license"], &licenseFields); err != nil {
		return err
	}
	if err := requireFields(licenseFields, "license_id", "customer", "product", "edition", "deployment_mode", "entitlements", "issued_at", "not_before", "expires_at", "installation_binding", "license_version", "key_id"); err != nil {
		return err
	}
	for field, required := range map[string][]string{
		"customer":             {"organization_id", "organization_name"},
		"entitlements":         {"features"},
		"installation_binding": {"type", "value"},
	} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(licenseFields[field], &nested); err != nil {
			return err
		}
		if err := requireFields(nested, required...); err != nil {
			return err
		}
	}
	var signatureFields map[string]json.RawMessage
	if err := json.Unmarshal(envelopeFields["signature"], &signatureFields); err != nil {
		return err
	}
	return requireFields(signatureFields, "algorithm", "value")
}

func requireFields(object map[string]json.RawMessage, fields ...string) error {
	if object == nil {
		return errors.New("expected JSON object")
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	return nil
}

func matchingDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}

func Load(path string, verifier Verifier) Evaluation {
	_, evaluation := LoadDocument(path, verifier)
	return evaluation
}

// LoadDocument reads a license once for subsequent offline re-evaluation.
// Re-evaluation lets expiry and clock rollback take effect without restarting
// Core; replacing the file still requires a controlled process restart.
func LoadDocument(path string, verifier Verifier) ([]byte, Evaluation) {
	if strings.TrimSpace(path) == "" {
		return nil, Evaluation{Status: entitlements.StatusCommunity}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Evaluation{Status: entitlements.StatusInvalid, Reason: "license_unreadable"}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxLicenseBytes+1))
	if err != nil {
		return nil, Evaluation{Status: entitlements.StatusInvalid, Reason: "license_unreadable"}
	}
	return data, verifier.Verify(data)
}

type Provider struct {
	Community  entitlements.CommunityProvider
	Evaluation Evaluation
	License    []byte
	Verifier   *Verifier
}

func (p Provider) Snapshot(ctx context.Context, workspaceID int64) entitlements.Snapshot {
	snapshot := p.Community.Snapshot(ctx, workspaceID)
	evaluation := p.Evaluation
	if p.Verifier != nil && len(p.License) != 0 {
		evaluation = p.Verifier.Verify(p.License)
	}
	if evaluation.Status == entitlements.StatusCommunity {
		return snapshot
	}
	if evaluation.Claims != nil {
		snapshot.License = &entitlements.LicenseMetadata{
			LicenseID: evaluation.Claims.LicenseID,
			KeyID:     evaluation.Claims.KeyID,
			ExpiresAt: evaluation.Claims.ExpiresAt,
			Reason:    evaluation.Reason,
		}
	} else {
		snapshot.License = &entitlements.LicenseMetadata{Reason: evaluation.Reason}
	}
	snapshot.Status = evaluation.Status
	if evaluation.Status != entitlements.StatusLicensed && evaluation.Status != entitlements.StatusGrace {
		return snapshot
	}
	snapshot.Edition = entitlements.EditionEnterprise
	for _, feature := range evaluation.Claims.Entitlements.Features {
		snapshot.Features[feature] = true
	}
	for limit, value := range evaluation.Claims.Entitlements.Limits {
		snapshot.Limits[limit] = value
	}
	return snapshot
}
