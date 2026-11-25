// SPDX-License-Identifier: AGPL-3.0-only

package alibaba

import (
	"context"
	"errors"
	"strings"

	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/providers"
	net "github.com/netriun/nexus/internal/providers/internal/network"
)

func WithNetwork(base providers.Provider, factory NetworkClientFactory) (providers.Provider, error) {
	if factory == nil {
		return nil, errors.New("Alibaba network client factory is required")
	}
	return net.New(base, "alibaba", func(ctx context.Context, account providers.Account, region string) ([]providers.Resource, []providers.Relation, error) {
		clients, err := factory(ctx, account, region)
		if err != nil {
			return nil, nil, err
		}
		if clients.VPC == nil || clients.SLB == nil || clients.ALB == nil || clients.ECS == nil {
			return nil, nil, errors.New("Alibaba network client is nil")
		}
		return listNetwork(ctx, clients, region)
	})
}
func networkTags(value any) map[string]string {
	if obj := networkObject(value); obj != nil {
		value = obj["Tag"]
	}
	tags := map[string]string{}
	for _, tag := range networkRows(value) {
		key := networkString(tag, "Key")
		value := networkString(tag, "Value")
		if key == "" {
			key = networkString(tag, "TagKey")
			value = networkString(tag, "TagValue")
		}
		if key != "" {
			tags[key] = value
		}
	}
	return tags
}
func listNetwork(ctx context.Context, c NetworkClients, region string) ([]providers.Resource, []providers.Relation, error) {
	items := []providers.Resource{}
	edges := []providers.Relation{}
	query := map[string]any{"RegionId": region}
	vpcs, err := networkPages(ctx, c.VPC, "DescribeVpcs", "2016-04-28", query, false, "Vpcs", "Vpc")
	if err != nil {
		return nil, nil, err
	}
	for _, v := range vpcs {
		item := net.Item("network", networkString(v, "VpcId"), networkString(v, "VpcName"), strings.ToLower(networkString(v, "Status")), "VPC", networkTags(v["Tags"]), v)
		item.Summary["cidr_block"] = networkString(v, "CidrBlock")
		items = append(items, item)
	}
	switches, err := networkPages(ctx, c.VPC, "DescribeVSwitches", "2016-04-28", query, false, "VSwitches", "VSwitch")
	if err != nil {
		return nil, nil, err
	}
	for _, v := range switches {
		id, vpc := networkString(v, "VSwitchId"), networkString(v, "VpcId")
		item := net.Item("subnet", id, networkString(v, "VSwitchName"), strings.ToLower(networkString(v, "Status")), "VSwitch", networkTags(v["Tags"]), v)
		item.Summary["cidr_block"] = networkString(v, "CidrBlock")
		item.Summary["vpc_id"] = vpc
		items = append(items, item)
		edges = append(edges, net.Edge(net.Ref(net.Service, "subnet", region, id), net.Ref(net.Service, "network", region, vpc), "in_network"))
	}
	clbs, err := networkPages(ctx, c.SLB, "DescribeLoadBalancers", "2014-05-15", query, false, "LoadBalancers", "LoadBalancer")
	if err != nil {
		return nil, nil, err
	}
	for _, lb := range clbs {
		id := networkString(lb, "LoadBalancerId")
		detail, err := networkCall(ctx, c.SLB, "DescribeLoadBalancerAttribute", "2014-05-15", map[string]any{"RegionId": region, "LoadBalancerId": id})
		if err != nil {
			return nil, nil, err
		}
		vpc := networkString(detail, "VpcId")
		items = append(items, net.Item("load_balancer", id, networkString(lb, "LoadBalancerName"), strings.ToLower(networkString(lb, "LoadBalancerStatus")), "CLB/SLB", networkTags(detail["Tags"]), detail))
		edges = append(edges, net.Edge(net.Ref(net.Service, "load_balancer", region, id), net.Ref(net.Service, "network", region, vpc), "in_network"))
		edges = append(edges, clbBackendEdges(networkRows(networkPath(detail, "BackendServers", "BackendServer")), region, id)...)
		for _, spec := range []struct{ list, attribute, container, element, key string }{{"DescribeVServerGroups", "DescribeVServerGroupAttribute", "VServerGroups", "VServerGroup", "VServerGroupId"}, {"DescribeMasterSlaveServerGroups", "DescribeMasterSlaveServerGroupAttribute", "MasterSlaveServerGroups", "MasterSlaveServerGroup", "MasterSlaveServerGroupId"}} {
			groups, err := networkCall(ctx, c.SLB, spec.list, "2014-05-15", map[string]any{"RegionId": region, "LoadBalancerId": id, "IncludeListener": true})
			if err != nil {
				return nil, nil, err
			}
			for _, group := range networkRows(networkPath(groups, spec.container, spec.element)) {
				if len(networkRows(networkPath(group, "AssociatedObjects", "Listeners", "Listener"))) == 0 {
					continue
				}
				groupBody, err := networkCall(ctx, c.SLB, spec.attribute, "2014-05-15", map[string]any{"RegionId": region, spec.key: networkString(group, spec.key)})
				if err != nil {
					return nil, nil, err
				}
				container, element := "BackendServers", "BackendServer"
				if spec.key == "MasterSlaveServerGroupId" {
					container = "MasterSlaveBackendServers"
					element = "MasterSlaveBackendServer"
				}
				edges = append(edges, clbBackendEdges(networkRows(networkPath(groupBody, container, element)), region, id)...)
			}
		}
	}
	albs, err := networkPages(ctx, c.ALB, "ListLoadBalancers", "2020-06-16", map[string]any{}, true, "LoadBalancers")
	if err != nil {
		return nil, nil, err
	}
	for _, lb := range albs {
		id, vpc := networkString(lb, "LoadBalancerId"), networkString(lb, "VpcId")
		items = append(items, net.Item("load_balancer", id, networkString(lb, "LoadBalancerName"), strings.ToLower(networkString(lb, "LoadBalancerStatus")), "ALB", networkTags(lb["Tags"]), lb))
		edges = append(edges, net.Edge(net.Ref(net.Service, "load_balancer", region, id), net.Ref(net.Service, "network", region, vpc), "in_network"))
		listeners, err := networkPages(ctx, c.ALB, "ListListeners", "2020-06-16", map[string]any{"LoadBalancerIds": []string{id}}, true, "Listeners")
		if err != nil {
			return nil, nil, err
		}
		rules, err := networkPages(ctx, c.ALB, "ListRules", "2020-06-16", map[string]any{"LoadBalancerIds": []string{id}}, true, "Rules")
		if err != nil {
			return nil, nil, err
		}
		groups := map[string]bool{}
		for _, listener := range listeners {
			albForwardGroups(networkRows(listener["DefaultActions"]), groups)
		}
		for _, rule := range rules {
			albForwardGroups(networkRows(rule["RuleActions"]), groups)
		}
		for group := range groups {
			servers, err := networkPages(ctx, c.ALB, "ListServerGroupServers", "2020-06-16", map[string]any{"ServerGroupId": group}, true, "Servers")
			if err != nil {
				return nil, nil, err
			}
			for _, server := range servers {
				if networkString(server, "ServerType") == "Ecs" {
					edges = append(edges, net.Edge(net.Ref("compute", "instance", region, networkString(server, "ServerId")), net.Ref(net.Service, "load_balancer", region, id), "behind_lb"))
				}
			}
		}
	}
	instances, err := cloud.AlibabaInventory(ctx, c.ECS, region)
	if err != nil {
		return nil, nil, err
	}
	return items, append(edges, net.InstanceEdges(instances, region)...), nil
}
func clbBackendEdges(servers []map[string]any, region, lb string) []providers.Relation {
	var edges []providers.Relation
	for _, server := range servers {
		if networkString(server, "Type") == "ecs" {
			edges = append(edges, net.Edge(net.Ref("compute", "instance", region, networkString(server, "ServerId")), net.Ref(net.Service, "load_balancer", region, lb), "behind_lb"))
		}
	}
	return edges
}
func albForwardGroups(actions []map[string]any, groups map[string]bool) {
	for _, action := range actions {
		if networkString(action, "Type") != "ForwardGroup" {
			continue
		}
		for _, tuple := range networkRows(networkPath(action, "ForwardGroupConfig", "ServerGroupTuples")) {
			if id := networkString(tuple, "ServerGroupId"); id != "" {
				groups[id] = true
			}
		}
	}
}
