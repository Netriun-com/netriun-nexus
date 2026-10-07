// SPDX-License-Identifier: AGPL-3.0-only

package enterprise

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	enterprisev1 "github.com/netriun/nexus/api/enterprise/v1"
)

type testPKI struct {
	caFile, clientCertFile, clientKeyFile string
	serverCertificate                     tls.Certificate
	clientCAs                             *x509.CertPool
}

func makeTestPKI(t *testing.T) testPKI {
	t.Helper()
	now := time.Now().UTC()
	return makeTestPKIWindow(t, now.Add(-time.Hour), now.Add(time.Hour))
}

func makeTestPKIWindow(t *testing.T, notBefore, notAfter time.Time) testPKI {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Nexus test CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(serial int64, name string, dns []string, usage x509.ExtKeyUsage) (tls.Certificate, []byte, []byte) {
		t.Helper()
		publicKey, privateKey, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: name},
			DNSNames:     dns,
			NotBefore:    notBefore,
			NotAfter:     notAfter,
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, createErr := x509.CreateCertificate(rand.Reader, template, caCertificate, publicKey, caPrivate)
		if createErr != nil {
			t.Fatal(createErr)
		}
		keyDER, marshalErr := x509.MarshalPKCS8PrivateKey(privateKey)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		certificate, pairErr := tls.X509KeyPair(certPEM, keyPEM)
		if pairErr != nil {
			t.Fatal(pairErr)
		}
		return certificate, certPEM, keyPEM
	}
	serverCertificate, _, _ := issue(2, "enterprise.test", []string{"enterprise.test"}, x509.ExtKeyUsageServerAuth)
	_, clientCertPEM, clientKeyPEM := issue(3, "nexus-core", nil, x509.ExtKeyUsageClientAuth)
	directory := t.TempDir()
	caFile := filepath.Join(directory, "ca.pem")
	clientCertFile := filepath.Join(directory, "client.pem")
	clientKeyFile := filepath.Join(directory, "client-key.pem")
	for path, data := range map[string][]byte{
		caFile:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		clientCertFile: clientCertPEM,
		clientKeyFile:  clientKeyPEM,
	} {
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(caCertificate)
	return testPKI{caFile: caFile, clientCertFile: clientCertFile, clientKeyFile: clientKeyFile, serverCertificate: serverCertificate, clientCAs: clientCAs}
}

func startMTLSServer(t *testing.T, pki testPKI, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pki.serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.clientCAs,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func newFileClient(t *testing.T, server *httptest.Server, pki testPKI) *Client {
	t.Helper()
	client, err := New(context.Background(), Config{
		URL:            server.URL,
		AuthMode:       AuthFiles,
		CAFile:         pki.caFile,
		ClientCertFile: pki.clientCertFile,
		ClientKeyFile:  pki.clientKeyFile,
		ServerName:     "enterprise.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestMTLSClientNegotiatesMajorAndRejectsUnauthenticatedCaller(t *testing.T) {
	pki := makeTestPKI(t)
	server := startMTLSServer(t, pki, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "nexus-core" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"netriun-nexus-enterprise","service_version":"1.0.0","api_majors":[1],"capabilities":[]}`))
	}))
	client := newFileClient(t, server, pki)
	if _, err := client.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get(server.URL); err == nil {
		t.Fatal("external caller without a client certificate reached Enterprise")
	}
	if _, err := (*Client)(nil).Info(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("optional Enterprise service must report unavailable, got %v", err)
	}
}

func TestCapabilityRequestDigestTimestampAndReplayProtection(t *testing.T) {
	pki := makeTestPKI(t)
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	seen := map[string]string{}
	server := startMTLSServer(t, pki, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		requestID := r.Header.Get(HeaderRequestID)
		if requestID == "" || r.Header.Get(HeaderIdempotencyKey) != requestID {
			http.Error(w, "missing idempotency identity", http.StatusUnauthorized)
			return
		}
		requestTime, err := time.Parse(time.RFC3339, r.Header.Get(HeaderRequestTime))
		if err != nil || requestTime.Before(now.Add(-5*time.Minute)) || requestTime.After(now.Add(5*time.Minute)) {
			http.Error(w, "stale request", http.StatusUnauthorized)
			return
		}
		digest := sha256.Sum256(body)
		expectedDigest := "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
		if r.Header.Get(HeaderContentDigest) != expectedDigest {
			http.Error(w, "digest mismatch", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		previous, exists := seen[requestID]
		if exists && previous != expectedDigest {
			mu.Unlock()
			http.Error(w, "request id reused with another payload", http.StatusConflict)
			return
		}
		seen[requestID] = expectedDigest
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"accepted":true}}`))
	}))
	client := newFileClient(t, server, pki)
	client.now = func() time.Time { return now }
	request := enterprisev1.CapabilityRequest{
		Context:    enterprisev1.InstallationContext{InstallationID: "ins_test", WorkspaceID: 1, RequestID: "req_0123456789abcdef"},
		Capability: "advanced_sso",
		Operation:  "sync_groups",
		Input:      map[string]any{"provider": "example"},
	}
	if _, err := client.Invoke(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Invoke(context.Background(), request); err != nil {
		t.Fatalf("idempotent replay of the same request must return its result: %v", err)
	}
	request.Input["provider"] = "modified"
	if _, err := client.Invoke(context.Background(), request); !errors.Is(err, ErrReplay) {
		t.Fatalf("request ID reuse with a modified body was not rejected: %v", err)
	}
	request.Context.RequestID = "req_abcdef0123456789"
	client.now = func() time.Time { return now.Add(-6 * time.Minute) }
	if _, err := client.Invoke(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale authenticated request was not rejected: %v", err)
	}
}

func TestClientRejectsInsecureOrIncompleteAuthentication(t *testing.T) {
	if _, err := New(context.Background(), Config{URL: "http://enterprise.local", AuthMode: AuthFiles}); err == nil {
		t.Fatal("plaintext Enterprise URL accepted")
	}
	if _, err := New(context.Background(), Config{URL: "https://enterprise.local", AuthMode: AuthFiles}); err == nil {
		t.Fatal("incomplete file mTLS configuration accepted")
	}
	if _, err := New(context.Background(), Config{URL: "https://enterprise.local", AuthMode: AuthSPIFFE, ServerSPIFFEID: "invalid"}); err == nil {
		t.Fatal("invalid SPIFFE server identity accepted")
	}
}

func TestMTLSRejectsWrongServerIdentityUntrustedAndExpiredCertificates(t *testing.T) {
	pki := makeTestPKI(t)
	server := startMTLSServer(t, pki, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"product":"enterprise","service_version":"1","api_majors":[1],"capabilities":[]}`))
	}))

	wrongName, err := New(context.Background(), Config{
		URL:            server.URL,
		AuthMode:       AuthFiles,
		CAFile:         pki.caFile,
		ClientCertFile: pki.clientCertFile,
		ClientKeyFile:  pki.clientKeyFile,
		ServerName:     "wrong-enterprise.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer wrongName.Close()
	if _, err = wrongName.Info(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong server identity was not rejected: %v", err)
	}

	untrustedClientPKI := makeTestPKI(t)
	untrustedClient, err := New(context.Background(), Config{
		URL:            server.URL,
		AuthMode:       AuthFiles,
		CAFile:         pki.caFile,
		ClientCertFile: untrustedClientPKI.clientCertFile,
		ClientKeyFile:  untrustedClientPKI.clientKeyFile,
		ServerName:     "enterprise.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer untrustedClient.Close()
	if _, err = untrustedClient.Info(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("untrusted client certificate was not rejected: %v", err)
	}

	now := time.Now().UTC()
	expiredPKI := makeTestPKIWindow(t, now.Add(-2*time.Hour), now.Add(-time.Hour))
	expiredServer := startMTLSServer(t, expiredPKI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"product":"enterprise","service_version":"1","api_majors":[1],"capabilities":[]}`))
	}))
	expiredClient := newFileClient(t, expiredServer, expiredPKI)
	if _, err = expiredClient.Info(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired certificate was not rejected: %v", err)
	}
}

func TestClientRejectsIncompatibleMajor(t *testing.T) {
	pki := makeTestPKI(t)
	server := startMTLSServer(t, pki, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(enterprisev1.ServiceInfo{Product: "enterprise", ServiceVersion: "2", APIMajors: []int{2}, Capabilities: []string{}})
	}))
	client := newFileClient(t, server, pki)
	if _, err := client.Info(context.Background()); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected incompatible API, got %v", err)
	}
}
