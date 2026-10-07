// SPDX-License-Identifier: AGPL-3.0-only

package enterprise

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	enterprisev1 "github.com/netriun/nexus/api/enterprise/v1"
)

const (
	HeaderRequestID      = "X-Netriun-Request-ID"
	HeaderRequestTime    = "X-Netriun-Request-Timestamp"
	HeaderContentDigest  = "Content-Digest"
	HeaderIdempotencyKey = "Idempotency-Key"
	MaxResponseBytes     = 1 << 20
)

var (
	ErrUnavailable   = errors.New("Enterprise service unavailable")
	ErrIncompatible  = errors.New("Enterprise service API major is incompatible")
	ErrUnauthorized  = errors.New("Enterprise service authentication failed")
	ErrReplay        = errors.New("Enterprise request replay rejected")
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
)

type AuthMode string

const (
	AuthSPIFFE AuthMode = "spiffe"
	AuthFiles  AuthMode = "files"
)

type Config struct {
	URL            string
	AuthMode       AuthMode
	SPIFFEEndpoint string
	ServerSPIFFEID string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	ServerName     string
	ConnectTimeout time.Duration
}

type Client struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
	close   func() error
}

func New(ctx context.Context, config Config) (*Client, error) {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return nil, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("ENTERPRISE_SERVICE_URL must be an https URL without credentials, query, or fragment")
	}
	connectTimeout := config.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 5 * time.Second
	}
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSHandshakeTimeout: connectTimeout}
	client := &Client{baseURL: strings.TrimRight(u.String(), "/"), now: time.Now}
	switch config.AuthMode {
	case AuthSPIFFE:
		serverID, parseErr := spiffeid.FromString(strings.TrimSpace(config.ServerSPIFFEID))
		if parseErr != nil {
			return nil, errors.New("ENTERPRISE_SPIFFE_SERVER_ID must be a valid SPIFFE ID")
		}
		sourceCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		defer cancel()
		options := []workloadapi.X509SourceOption{}
		if endpoint := strings.TrimSpace(config.SPIFFEEndpoint); endpoint != "" {
			options = append(options, workloadapi.WithClientOptions(workloadapi.WithAddr(endpoint)))
		}
		source, sourceErr := workloadapi.NewX509Source(sourceCtx, options...)
		if sourceErr != nil {
			return nil, fmt.Errorf("SPIFFE X.509 source unavailable: %w", sourceErr)
		}
		transport.TLSClientConfig = tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeID(serverID))
		transport.TLSClientConfig.MinVersion = tls.VersionTLS13
		client.close = source.Close
	case AuthFiles:
		tlsConfig, configErr := fileTLSConfig(config)
		if configErr != nil {
			return nil, configErr
		}
		transport.TLSClientConfig = tlsConfig
	default:
		return nil, errors.New("ENTERPRISE_AUTH_MODE must be spiffe or files when Enterprise is configured")
	}
	client.http = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return client, nil
}

func fileTLSConfig(config Config) (*tls.Config, error) {
	if strings.TrimSpace(config.CAFile) == "" || strings.TrimSpace(config.ClientCertFile) == "" || strings.TrimSpace(config.ClientKeyFile) == "" || strings.TrimSpace(config.ServerName) == "" {
		return nil, errors.New("file mTLS requires CA, client certificate, client key, and server name")
	}
	caPEM, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read Enterprise CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Enterprise CA file contains no certificate")
	}
	certificate, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Enterprise client identity: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		ServerName:   config.ServerName,
	}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.http != nil {
		if transport, ok := c.http.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
	if c.close != nil {
		return c.close()
	}
	return nil
}

func (c *Client) Info(ctx context.Context) (enterprisev1.ServiceInfo, error) {
	var info enterprisev1.ServiceInfo
	if c == nil {
		return info, ErrUnavailable
	}
	if err := c.doJSON(ctx, http.MethodGet, c.baseURL+enterprisev1.BasePath+"/info", nil, "", &info); err != nil {
		return info, err
	}
	for _, major := range info.APIMajors {
		if major == enterprisev1.MajorVersion {
			return info, nil
		}
	}
	return info, ErrIncompatible
}

func (c *Client) Invoke(ctx context.Context, request enterprisev1.CapabilityRequest) (enterprisev1.CapabilityResponse, error) {
	var response enterprisev1.CapabilityResponse
	if c == nil {
		return response, ErrUnavailable
	}
	if !requestIDPattern.MatchString(request.Context.RequestID) || request.Context.InstallationID == "" || request.Context.WorkspaceID < 1 || !validPathPart(request.Capability) || !validPathPart(request.Operation) {
		return response, errors.New("invalid Enterprise capability request")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	endpoint := c.baseURL + enterprisev1.BasePath + "/capabilities/" + url.PathEscape(request.Capability) + "/" + url.PathEscape(request.Operation)
	return response, c.doJSON(ctx, http.MethodPost, endpoint, body, request.Context.RequestID, &response)
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body []byte, requestID string, output any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		digest := sha256.Sum256(body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(HeaderRequestID, requestID)
		req.Header.Set(HeaderIdempotencyKey, requestID)
		req.Header.Set(HeaderRequestTime, c.now().UTC().Truncate(time.Second).Format(time.RFC3339))
		req.Header.Set(HeaderContentDigest, "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrReplay
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil {
		return fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	return nil
}

var pathPartPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

func validPathPart(value string) bool { return pathPartPattern.MatchString(value) }
