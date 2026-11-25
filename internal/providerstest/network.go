// SPDX-License-Identifier: AGPL-3.0-only

// Package providerstest replays synthetic API-shaped network fixtures. All
// transports fail closed and never use sockets, credentials or metadata APIs.
package providerstest

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/netriun/nexus/internal/providers"
	"github.com/netriun/nexus/internal/providers/alibaba"
	"github.com/netriun/nexus/internal/providers/aws"
)

//go:embed testdata/*.json
var fixtures embed.FS

type Replay struct {
	Provider     string
	Empty        bool
	FailAction   string
	FailPage     int
	RepeatAction string
	CancelAction string
	Cancel       context.CancelFunc
	mu           sync.Mutex
	Calls        map[string]int
	data         map[string][]json.RawMessage
}

func New(t *testing.T, provider string) *Replay {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/" + provider + ".json")
	if err != nil {
		t.Fatal(err)
	}
	r := &Replay{Provider: provider, Calls: map[string]int{}}
	if err := json.Unmarshal(raw, &r.data); err != nil {
		t.Fatal(err)
	}
	return r
}
func (r *Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := req.URL.Query()
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body, err := url.ParseQuery(string(raw))
		if err != nil {
			return nil, err
		}
		for key, values := range body {
			query[key] = values
		}
	}
	action := query.Get("Action")
	if action == "" {
		for key, values := range req.Header {
			if strings.EqualFold(key, "x-acs-action") && len(values) > 0 {
				action = values[0]
			}
		}
	}
	pages, ok := r.data[action]
	if !ok {
		return nil, fmt.Errorf("unexpected action %q", action)
	}
	host := req.URL.Host
	if r.Provider == "aws" {
		if host != "ec2.us-east-1.amazonaws.com" && host != "elasticloadbalancing.us-east-1.amazonaws.com" {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
	} else if host != "vpc.cn-hangzhou.aliyuncs.com" && host != "slb.aliyuncs.com" && host != "alb.cn-hangzhou.aliyuncs.com" && host != "ecs-cn-hangzhou.aliyuncs.com" {
		return nil, fmt.Errorf("unexpected host %q", host)
	}
	if strings.Contains(action, "VServerGroups") || action == "DescribeMasterSlaveServerGroups" {
		if query.Get("IncludeListener") != "true" {
			return nil, fmt.Errorf("missing listener association option")
		}
	}
	if query.Get("VServerGroupId") == "vsg-unused" || query.Get("ServerGroupId") == "must-not-be-a-backend" {
		return nil, fmt.Errorf("unused group incorrectly treated as a backend")
	}
	page := 1
	token := query.Get("NextToken")
	if token == "" {
		token = query.Get("Marker")
	}
	if token != "" && token != "next" {
		return nil, fmt.Errorf("unexpected pagination token %q", token)
	}
	if token == "next" || query.Get("PageNumber") == "2" {
		page = 2
	}
	if query.Get("PageNumber") != "" && query.Get("PageNumber") != "1" && query.Get("PageNumber") != "2" {
		return nil, fmt.Errorf("unexpected page number")
	}
	if page > len(pages) {
		return nil, fmt.Errorf("unexpected page %d for %s", page, action)
	}
	r.Calls[action]++
	status, contentType, body := 200, "application/json", ""
	if r.CancelAction == action && page == 2 {
		r.Cancel()
		return nil, context.Canceled
	}
	if r.FailAction == action && page == r.FailPage {
		status = 403
		if r.Provider == "aws" {
			body = `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>fixture access denied</Message></Error></Errors><RequestID>fixture</RequestID></Response>`
		} else {
			body = `{"Code":"Forbidden","Message":"fixture access denied","RequestId":"fixture"}`
		}
	} else if r.Provider == "aws" {
		if err := json.Unmarshal(pages[page-1], &body); err != nil {
			return nil, err
		}
		contentType = "text/xml"
		if r.RepeatAction == action && page == 2 {
			end := "</" + action + "Response>"
			if strings.HasPrefix(action, "DescribeV") || action == "DescribeSubnets" || action == "DescribeInstances" {
				body = strings.Replace(body, end, "<nextToken>next</nextToken>"+end, 1)
			} else {
				body = strings.Replace(body, "</"+action+"Result>", "<NextMarker>next</NextMarker></"+action+"Result>", 1)
			}
		}
	} else {
		body = string(pages[page-1])
		if r.RepeatAction == action && page == 2 {
			var obj map[string]any
			if err := json.Unmarshal([]byte(body), &obj); err != nil {
				return nil, err
			}
			obj["NextToken"] = "next"
			raw, _ := json.Marshal(obj)
			body = string(raw)
		}
	}
	if r.Empty {
		if r.Provider == "aws" {
			field := map[string]string{"DescribeVpcs": "vpcSet", "DescribeSubnets": "subnetSet", "DescribeInstances": "reservationSet"}[action]
			if field != "" {
				body = "<" + action + "Response xmlns=\"http://ec2.amazonaws.com/doc/2016-11-15/\"><" + field + "/></" + action + "Response>"
			} else if action == "DescribeLoadBalancers" {
				body = "<DescribeLoadBalancersResponse xmlns=\"http://elasticloadbalancing.amazonaws.com/doc/2015-12-01/\"><DescribeLoadBalancersResult><LoadBalancers/></DescribeLoadBalancersResult></DescribeLoadBalancersResponse>"
			}
		} else {
			empty := map[string]string{"DescribeVpcs": `{"TotalCount":0,"Vpcs":{"Vpc":[]}}`, "DescribeVSwitches": `{"TotalCount":0,"VSwitches":{"VSwitch":[]}}`, "DescribeLoadBalancers": `{"TotalCount":0,"LoadBalancers":{"LoadBalancer":[]}}`, "ListLoadBalancers": `{"LoadBalancers":[]}`, "DescribeInstances": `{"Instances":{"Instance":[]}}`}
			if value, ok := empty[action]; ok {
				body = value
			}
		}
	}
	lb := query.Get("LoadBalancerId")
	if lb == "" {
		lb = query.Get("ResourceArns.member.1")
	}
	body = strings.ReplaceAll(body, "$LB", lb)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
func (r *Replay) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	return r.RoundTrip(req)
}

func Adapter(t *testing.T, provider string) (providers.Provider, *Replay) {
	t.Helper()
	r := New(t, provider)
	if provider == "aws" {
		client := ec2.NewFromConfig(sdkaws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fixture", "fixture", ""), RetryMaxAttempts: 1, HTTPClient: &http.Client{Transport: r}})
		base, err := aws.New(func(context.Context, providers.Account, string) (*ec2.Client, error) { return client, nil })
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := aws.WithNetwork(base, func(context.Context, providers.Account, string) (aws.NetworkClients, error) {
			return aws.NetworkClientsFor(client), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return adapter, r
	}
	clients, err := alibaba.NewNetworkClients("cn-hangzhou", "fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	clients.VPC.HttpClient = r
	clients.SLB.HttpClient = r
	clients.ALB.HttpClient = r
	clients.ECS.HttpClient = r
	base, err := alibaba.New(func(context.Context, providers.Account, string) (*ecs.Client, error) { return clients.ECS, nil })
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := alibaba.WithNetwork(base, func(context.Context, providers.Account, string) (alibaba.NetworkClients, error) { return clients, nil })
	if err != nil {
		t.Fatal(err)
	}
	return adapter, r
}
