// SPDX-License-Identifier: AGPL-3.0-only

package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	net "github.com/netriun/nexus/internal/providers/internal/network"
)

type NetworkClients struct {
	EC2 *ec2.Client
	ELB *elasticloadbalancingv2.Client
}
type NetworkClientFactory func(context.Context, providers.Account, string) (NetworkClients, error)

// NetworkClientsFor reuses the existing EC2 client's account credentials and
// transport. The ELB SDK independently resolves the regional ELBv2 endpoint.
func NetworkClientsFor(client *ec2.Client) NetworkClients {
	options := client.Options()
	return NetworkClients{EC2: client, ELB: elasticloadbalancingv2.NewFromConfig(sdkaws.Config{Region: options.Region, Credentials: options.Credentials, HTTPClient: options.HTTPClient, RetryMaxAttempts: options.RetryMaxAttempts})}
}
func WithNetwork(base providers.Provider, factory NetworkClientFactory) (providers.Provider, error) {
	if factory == nil {
		return nil, errors.New("AWS network client factory is required")
	}
	return net.New(base, "aws", func(ctx context.Context, account providers.Account, region string) ([]providers.Resource, []providers.Relation, error) {
		clients, err := factory(ctx, account, region)
		if err != nil {
			return nil, nil, err
		}
		if clients.EC2 == nil || clients.ELB == nil {
			return nil, nil, errors.New("AWS network client is nil")
		}
		return listNetwork(ctx, clients, region)
	})
}
func listNetwork(ctx context.Context, c NetworkClients, region string) ([]providers.Resource, []providers.Relation, error) {
	items := []providers.Resource{}
	edges := []providers.Relation{}
	vpcs := ec2.NewDescribeVpcsPaginator(c.EC2, &ec2.DescribeVpcsInput{})
	seen := map[string]bool{}
	for vpcs.HasMorePages() {
		page, err := vpcs.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := net.CheckToken(seen, sdkaws.ToString(page.NextToken)); err != nil {
			return nil, nil, err
		}
		for _, v := range page.Vpcs {
			tags := map[string]string{}
			for _, tag := range v.Tags {
				tags[sdkaws.ToString(tag.Key)] = sdkaws.ToString(tag.Value)
			}
			item := net.Item("network", sdkaws.ToString(v.VpcId), tags["Name"], string(v.State), "VPC", tags, map[string]any{"cidr_block": sdkaws.ToString(v.CidrBlock), "is_default": sdkaws.ToBool(v.IsDefault)})
			item.Summary["cidr_block"] = sdkaws.ToString(v.CidrBlock)
			items = append(items, item)
		}
	}
	subnets := ec2.NewDescribeSubnetsPaginator(c.EC2, &ec2.DescribeSubnetsInput{})
	seen = map[string]bool{}
	for subnets.HasMorePages() {
		page, err := subnets.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := net.CheckToken(seen, sdkaws.ToString(page.NextToken)); err != nil {
			return nil, nil, err
		}
		for _, v := range page.Subnets {
			tags := map[string]string{}
			for _, tag := range v.Tags {
				tags[sdkaws.ToString(tag.Key)] = sdkaws.ToString(tag.Value)
			}
			id, vpc := sdkaws.ToString(v.SubnetId), sdkaws.ToString(v.VpcId)
			item := net.Item("subnet", id, tags["Name"], string(v.State), "Subnet", tags, map[string]any{"vpc_id": vpc, "cidr_block": sdkaws.ToString(v.CidrBlock), "zone": sdkaws.ToString(v.AvailabilityZone), "available_ip_address_count": v.AvailableIpAddressCount})
			item.Summary["cidr_block"] = sdkaws.ToString(v.CidrBlock)
			item.Summary["vpc_id"] = vpc
			items = append(items, item)
			edges = append(edges, net.Edge(net.Ref(net.Service, "subnet", region, id), net.Ref(net.Service, "network", region, vpc), "in_network"))
		}
	}
	lbs := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(c.ELB, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	seen = map[string]bool{}
	for lbs.HasMorePages() {
		page, err := lbs.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := net.CheckToken(seen, sdkaws.ToString(page.NextMarker)); err != nil {
			return nil, nil, err
		}
		for _, lb := range page.LoadBalancers {
			id, vpc := sdkaws.ToString(lb.LoadBalancerArn), sdkaws.ToString(lb.VpcId)
			tagPage, err := c.ELB.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{id}})
			if err != nil {
				return nil, nil, err
			}
			tags := map[string]string{}
			for _, description := range tagPage.TagDescriptions {
				for _, tag := range description.Tags {
					tags[sdkaws.ToString(tag.Key)] = sdkaws.ToString(tag.Value)
				}
			}
			state := "unknown"
			if lb.State != nil {
				state = string(lb.State.Code)
			}
			item := net.Item("load_balancer", id, sdkaws.ToString(lb.LoadBalancerName), state, "ELBv2 "+string(lb.Type), tags, map[string]any{"vpc_id": vpc, "dns_name": sdkaws.ToString(lb.DNSName), "scheme": lb.Scheme, "availability_zones": lb.AvailabilityZones})
			item.Summary["vpc_id"] = vpc
			item.Summary["dns_name"] = sdkaws.ToString(lb.DNSName)
			items = append(items, item)
			edges = append(edges, net.Edge(net.Ref(net.Service, "load_balancer", region, id), net.Ref(net.Service, "network", region, vpc), "in_network"))
			groups := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(c.ELB, &elasticloadbalancingv2.DescribeTargetGroupsInput{LoadBalancerArn: lb.LoadBalancerArn})
			groupTokens := map[string]bool{}
			for groups.HasMorePages() {
				groupPage, err := groups.NextPage(ctx)
				if err != nil {
					return nil, nil, err
				}
				if err := net.CheckToken(groupTokens, sdkaws.ToString(groupPage.NextMarker)); err != nil {
					return nil, nil, err
				}
				for _, group := range groupPage.TargetGroups {
					if group.TargetType != "instance" {
						continue
					}
					health, err := c.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{TargetGroupArn: group.TargetGroupArn})
					if err != nil {
						return nil, nil, err
					}
					for _, target := range health.TargetHealthDescriptions {
						if target.Target != nil {
							edges = append(edges, net.Edge(net.Ref("compute", "instance", region, sdkaws.ToString(target.Target.Id)), net.Ref(net.Service, "load_balancer", region, id), "behind_lb"))
						}
					}
				}
			}
		}
	}
	instances, err := cloud.Inventory(ctx, c.EC2)
	if err != nil {
		return nil, nil, err
	}
	return items, append(edges, net.InstanceEdges(instances, region)...), nil
}
