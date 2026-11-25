// SPDX-License-Identifier: AGPL-3.0-only

package alibaba

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	util "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	net "github.com/netriun/nexus/internal/providers/internal/network"
)

type NetworkClients struct {
	VPC, SLB, ALB *openapi.Client
	ECS           *ecs.Client
}
type NetworkClientFactory func(context.Context, providers.Account, string) (NetworkClients, error)

// NewNetworkClients uses the existing OpenAPI SDK's signing, STS credentials,
// transport and context support. Endpoints are the documented regional public
// endpoints; the RPC operation allow-list lives entirely in network.go.
func NewNetworkClients(region, key, secret, token string) (NetworkClients, error) {
	if region == "" {
		return NetworkClients{}, errors.New("Alibaba network region is required")
	}
	for _, r := range region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return NetworkClients{}, errors.New("invalid Alibaba network region")
		}
	}
	clients := NetworkClients{}
	for product, target := range map[string]**openapi.Client{"vpc": &clients.VPC, "slb": &clients.SLB, "alb": &clients.ALB} {
		endpoint := product + "." + region + ".aliyuncs.com"
		if product == "slb" && region == "cn-hangzhou" {
			endpoint = "slb.aliyuncs.com"
		}
		config := new(util.Config).SetRegionId(region).SetAccessKeyId(key).SetAccessKeySecret(secret).SetProtocol("HTTPS").SetEndpoint(endpoint)
		if token != "" {
			config.SetSecurityToken(token)
		}
		client, err := openapi.NewClient(config)
		if err != nil {
			return NetworkClients{}, err
		}
		*target = client
	}
	client, err := cloud.AlibabaClient(region, key, secret, token)
	if err != nil {
		return NetworkClients{}, err
	}
	clients.ECS = client
	return clients, nil
}

func networkCall(ctx context.Context, client *openapi.Client, action, version string, query map[string]any) (map[string]any, error) {
	params := &util.Params{Action: dara.String(action), Version: dara.String(version), Protocol: dara.String("HTTPS"), Pathname: dara.String("/"), Method: dara.String("POST"), AuthType: dara.String("AK"), Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("json")}
	out, err := client.CallApiWithCtx(ctx, params, &util.OpenApiRequest{Query: util.Query(query)}, &dara.RuntimeOptions{ReadTimeout: dara.Int(20000), ConnectTimeout: dara.Int(20000), Autoretry: dara.Bool(true), MaxAttempts: dara.Int(3)})
	if err != nil {
		return nil, err
	}
	body, ok := out["body"].(map[string]any)
	if !ok {
		return nil, errors.New("Alibaba network API returned no body")
	}
	return body, nil
}
func networkString(obj map[string]any, key string) string {
	value, _ := obj[key].(string)
	return value
}
func networkObject(value any) map[string]any { obj, _ := value.(map[string]any); return obj }
func networkRows(value any) []map[string]any {
	list, _ := value.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, value := range list {
		if obj := networkObject(value); obj != nil {
			out = append(out, obj)
		}
	}
	return out
}
func networkPath(body map[string]any, path ...string) any {
	var value any = body
	for _, key := range path {
		value = networkObject(value)[key]
	}
	return value
}
func networkPages(ctx context.Context, client *openapi.Client, action, version string, query map[string]any, token bool, path ...string) ([]map[string]any, error) {
	query = maps.Clone(query)
	result := []map[string]any{}
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if token {
			query["MaxResults"] = 100
		} else {
			query["PageNumber"] = page
			query["PageSize"] = 50
		}
		body, err := networkCall(ctx, client, action, version, query)
		if err != nil {
			return nil, err
		}
		rows := networkRows(networkPath(body, path...))
		result = append(result, rows...)
		if token {
			next := networkString(body, "NextToken")
			if next == "" {
				return result, nil
			}
			if err := net.CheckToken(seen, next); err != nil {
				return nil, err
			}
			query["NextToken"] = next
		} else {
			total, err := strconv.Atoi(fmt.Sprint(body["TotalCount"]))
			if err != nil || total < 0 {
				return nil, errors.New("Alibaba network API returned invalid total count")
			}
			if len(result) >= total {
				return result, nil
			}
			if len(rows) == 0 {
				return nil, errors.New("Alibaba network pagination ended early")
			}
		}
	}
}
