// SPDX-License-Identifier: AGPL-3.0-only

// Package alibabatest replays synthetic provider responses for adapter and
// PostgreSQL sync tests. It never delegates requests to a network transport.
package alibabatest

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
)

//go:embed fixtures/*
var fixtures embed.FS

type Replay struct {
	mu              sync.Mutex
	Account         providers.Account
	Service, Region string
	mode            string
	calls           map[string]int
	afterPage       func()
}

func New(account providers.Account, service, region string) *Replay {
	return &Replay{Account: account, Service: service, Region: region, calls: map[string]int{}}
}
func (r *Replay) Reset(mode string, afterPage func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode, r.afterPage, r.calls = mode, afterPage, map[string]int{}
}
func (r *Replay) Calls(key string) int { r.mu.Lock(); defer r.mu.Unlock(); return r.calls[key] }

func (r *Replay) ECS(_ context.Context, account providers.Account, region string) (*ecs.Client, error) {
	if account != r.Account || region != r.Region {
		return nil, fmt.Errorf("unexpected ECS factory scope")
	}
	c, err := cloud.AlibabaClient(region, "fixture", "fixture", "")
	if err == nil {
		c.HttpClient = r
	}
	return c, err
}
func (r *Replay) EDS(_ context.Context, account providers.Account, region string) (*cloud.EDSClients, error) {
	expected := r.Region
	if r.Service == providers.EDSUsers {
		expected = cloud.EDSUserRegion(expected)
	}
	if account != r.Account || region != expected {
		return nil, fmt.Errorf("unexpected EDS factory scope")
	}
	c, err := cloud.EDSClient(region, "fixture", "fixture", "")
	if err == nil {
		c.Desktop.HttpClient = r
		c.User.HttpClient = r
	}
	return c, err
}
func (r *Replay) OSS(_ context.Context, account providers.Account, region string) (*oss.Client, error) {
	if account != r.Account || (region != "*" && region != "cn-hangzhou" && region != "ap-southeast-1") {
		return nil, fmt.Errorf("unexpected OSS factory scope")
	}
	if region == "*" {
		region = "cn-hangzhou"
	}
	cfg := oss.LoadDefaultConfig().WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fixture", "fixture")).WithRegion(region).WithRetryMaxAttempts(1).WithHttpClient(&http.Client{Transport: r})
	return oss.NewClient(cfg), nil
}
func (r *Replay) Adapter(t *testing.T) providers.Provider {
	t.Helper()
	a, err := alibaba.NewWithServices(r.ECS, r.EDS, r.OSS)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (r *Replay) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	return r.RoundTrip(req)
}
func (r *Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	action := req.Header.Get("x-acs-action")
	if action == "" {
		action = req.URL.Query().Get("Action")
	}
	if action == "" {
		for k, v := range req.Header {
			if strings.EqualFold(k, "x-acs-action") && len(v) > 0 {
				action = v[0]
			}
		}
	}
	query := req.URL.Query()
	key, fixture, token, ext := "", "", query.Get("NextToken"), ".json"
	paginated := true
	switch r.Service {
	case providers.EDSDesktops:
		if action != "DescribeDesktops" || query.Get("RegionId") != r.Region || query.Get("MaxResults") != "100" || req.URL.Host != "ecd."+r.Region+".aliyuncs.com" {
			return nil, fmt.Errorf("unexpected desktop request: %s %s", action, req.URL)
		}
		key = "desktops"
	case providers.EDSUsers:
		if action != "DescribeUsers" || query.Get("MaxResults") != "100" || req.URL.Host != "eds-user."+cloud.EDSUserRegion(r.Region)+".aliyuncs.com" {
			return nil, fmt.Errorf("unexpected directory request: %s %s", action, req.URL)
		}
		key = "users"
	case providers.SecurityGroups:
		if query.Get("RegionId") != r.Region || req.URL.Host != "ecs-"+r.Region+".aliyuncs.com" {
			return nil, fmt.Errorf("unexpected security group scope: %s", req.URL)
		}
		switch action {
		case "DescribeSecurityGroups":
			key = "groups"
			if query.Get("MaxResults") != "100" {
				return nil, fmt.Errorf("wrong group page size")
			}
		case "DescribeSecurityGroupAttribute":
			id := query.Get("SecurityGroupId")
			if (id != "sg-one" && id != "sg-two") || query.Get("Direction") != "all" || query.Get("MaxResults") != "1000" {
				return nil, fmt.Errorf("unexpected rule request")
			}
			key = "rules:" + id
		default:
			return nil, fmt.Errorf("unexpected security group action: %s", action)
		}
	case providers.OSSBuckets:
		ext = ".xml"
		token = query.Get("marker")
		if req.Method != "GET" || req.URL.Path != "/" {
			return nil, fmt.Errorf("unexpected OSS request")
		}
		if req.URL.Host == "oss-cn-hangzhou.aliyuncs.com" {
			key = "buckets"
		} else {
			name, region := "fixture-one", "cn-hangzhou"
			if req.URL.Host == "fixture-two.oss-ap-southeast-1.aliyuncs.com" {
				name, region = "fixture-two", "ap-southeast-1"
			}
			if req.URL.Host != name+".oss-"+region+".aliyuncs.com" {
				return nil, fmt.Errorf("unexpected bucket endpoint: %s", req.URL.Host)
			}
			paginated = false
			if query.Has("bucketInfo") {
				key = "info:" + name
				fixture = "info-" + name
			} else if query.Has("stat") {
				key = "stat:" + name
				fixture = "stat"
			} else {
				return nil, fmt.Errorf("unexpected OSS detail request")
			}
		}
	default:
		return nil, fmt.Errorf("unexpected service: %s", r.Service)
	}
	r.calls[key]++
	page := r.calls[key]
	base := strings.Split(key, ":")[0]
	status := 200
	if paginated {
		expected := ""
		if page == 2 {
			expected = "page-two"
		}
		if page > 2 || token != expected {
			return nil, fmt.Errorf("invalid %s page %d token %q", key, page, token)
		}
		fixture = fmt.Sprintf("%s-%d", base, page)
		if r.mode == "empty" {
			fixture = base + "-empty"
		}
		if page == 2 && ((r.mode == "error-page" && base != "rules") || (r.mode == "rules-error" && key == "rules:sg-one")) {
			fixture = "error"
			status = 403
		}
		if page == 2 && base != "rules" && r.afterPage != nil {
			r.afterPage()
		}
	} else if (r.mode == "info-error" && key == "info:fixture-two") || (r.mode == "stat-error" && key == "stat:fixture-two") {
		fixture = "error"
		status = 403
	}
	raw, err := fixtures.ReadFile("fixtures/" + fixture + ext)
	if err != nil {
		return nil, err
	}
	contentType := "application/json"
	if ext == ".xml" {
		contentType = "application/xml"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: req}, nil
}
