// SPDX-License-Identifier: AGPL-3.0-only

package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	enterprisev1 "github.com/netriun/nexus/api/enterprise/v1"
)

var (
	ErrUnavailable  = errors.New("Enterprise service unavailable")
	ErrIncompatible = errors.New("Enterprise service API major is incompatible")
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(rawURL string, client *http.Client) (*Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("ENTERPRISE_SERVICE_URL must be an http(s) URL without credentials, query, or fragment")
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(u.String(), "/"), http: client}, nil
}

func (c *Client) Info(ctx context.Context) (enterprisev1.ServiceInfo, error) {
	var info enterprisev1.ServiceInfo
	if c == nil {
		return info, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+enterprisev1.BasePath+"/info", nil)
	if err != nil {
		return info, ErrUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return info, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&info); err != nil {
		return info, fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return info, fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	for _, major := range info.APIMajors {
		if major == enterprisev1.MajorVersion {
			return info, nil
		}
	}
	return info, ErrIncompatible
}
