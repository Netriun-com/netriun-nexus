# Regional network sync

AWS and Alibaba expose `network`, a regional service with kinds `network`,
`subnet`, and `load_balancer`. `WithNetwork` wraps the existing provider so
compute and Alibaba service adapters can evolve independently. Alibaba's T22
implementation is confined to `network*.go`; it does not edit the compute
adapter. Azure and GCP are unchanged.

`app.NewCollector` constructs the registry once per worker. Network factories
resolve encrypted credentials through the existing workspace/account/provider
lookup. AWS reuses the EC2 credentials and transport for ELBv2; Alibaba uses
the existing OpenAPI SDK's signed, context-aware RPC transport for VPC, CLB and
ALB, plus the existing ECS collector. There are no provider calls on read paths.

## Scheduling and persistence

Migration 017 adds `network` to the sync-state service constraint. Reconciliation
creates one row per configured or discovered AWS/Alibaba region. Upgrades reuse
known compute regions instead of waiting for discovery. Failed discovery keeps
known scopes; disabled rows retain their identity and state.

`NEXUS_SYNC_NETWORK_INTERVAL` defaults to `15m`. Helm exposes
`sync.networkIntervalSeconds` (default `900`) and emits the same variable for
web/account saves and worker replicas. Successful empty regions use the existing
`NEXUS_SYNC_EMPTY_INTERVAL`; errors retain the normal service interval.

A network collection is complete only after all required pages, backend lookups
and instance inventory calls succeed. Any API error, cancellation or repeated
token returns no partial data. The job locks the account, checks its lease and
sync-state generation, reconciles all three kinds, resolves relations, checks
the lease again, and commits **one** transaction. No instances or service-snapshot
rows are written. An empty successful result removes only this account's network
resources and owned relations in that region; failures preserve the old catalog.

The existing sync-state endpoint accepts `service=network`, with its existing
workspace/account-view authorization. Resource list/detail endpoints already
support the catalog kinds. No new HTTP routes or network editing UI are added.

## Normalization and topology

| Shared kind | AWS native type / identity | Alibaba native type / identity |
| --- | --- | --- |
| `network` | VPC / VPC ID | VPC / VPC ID |
| `subnet` | Subnet / subnet ID | VSwitch / VSwitch ID |
| `load_balancer` | ELBv2 application/network/gateway / ARN | CLB/SLB or ALB / load balancer ID |

Names, states and tags are normalized; `summary.provider_type` retains native
terminology. CIDRs, VPC IDs, DNS names and zones live in summary/details as supplied by
the corresponding API. Backend membership is represented by relations. `Get` scans the same
complete regional inventory, like the existing compute adapter, and is not
wired to an HTTP provider read path.

Provider references carry service, region, kind and provider ID. Edges are:

- `subnet in_network network`
- `load_balancer in_network network`
- `instance in_subnet subnet`
- `instance uses_rule security_group`
- `instance behind_lb load_balancer`

Instance subnet/group references come from the existing EC2/ECS inventory.
Security-group references target service `ecs.security_groups`. Missing catalog
endpoints (including groups not yet collected) are skipped and can resolve on
a later network sync. No placeholder resources or instance writes are invented.

AWS backend membership comes from load-balancer-associated target groups and
registered instance targets. Alibaba CLB includes its default backend group,
listener-associated vServer groups and primary/secondary groups; unassociated
groups are ignored. ALB includes forwarding groups referenced by listener
default actions and forwarding rules. Only ECS/EC2 instance IDs form instance
edges: IP, ENI, ECI and function targets are not guessed to be instances.
Multiple paths to the same backend edge are deduplicated.

Migration 017 also adds `resource_relations.owner_service`. Existing edges are
backfilled from their source resource's service. Ordinary resource reconciliation
replaces only that service's own outgoing edges; network collection can own
instance-source edges without the next compute sync erasing them. Network
reconciliation replaces its complete regional edge set after all kinds exist.
Endpoint deletion still cascades to edges. This internal ownership column is
not exposed in the relation API.

## Permissions, scope and limits

The credential needs read access to **all** operations below, including the
instance inventory used for topology. AWS adds the `elasticloadbalancing:Describe*`
operations to its EC2 reads. Alibaba needs the corresponding `vpc:`, `slb:`,
`alb:` and `ecs:DescribeInstances` RAM actions. Use the official action-specific
resource rules; a missing permission fails the whole region rather than
replacing healthy rows with partial data. All configured APIs must be available
in the selected region. Unsupported products/endpoints also fail that region.

CLB/ALB backend enumeration adds API requests per load balancer and server
group. Calls use existing SDK retries/timeouts under the scheduled collection
context. Alibaba's existing ECS inventory still checks cancellation between
pages; its completed result is discarded if the job has been cancelled.
No NLB-specific Alibaba service, AWS Classic ELB, listener resources, IP-target
inference, create/update/delete operations, or new queue is included.

## Official API references

Every operation below is read-only. API-shaped synthetic fixtures replay these
responses entirely in memory, including error pages and pagination. Tests need
neither live credentials nor external network access.

AWS:

- [EC2 DescribeVpcs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpcs.html)
- [EC2 DescribeSubnets](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSubnets.html)
- [EC2 DescribeInstances](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstances.html)
- [ELBv2 DescribeLoadBalancers](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancers.html)
- [ELBv2 DescribeTags](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTags.html)
- [ELBv2 DescribeTargetGroups](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTargetGroups.html)
- [ELBv2 DescribeTargetHealth](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTargetHealth.html)

Alibaba VPC (`2016-04-28`):

- [DescribeVpcs](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-describevpcs)
- [DescribeVSwitches](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-describevswitches)
- [Regional endpoints](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-endpoint)

Alibaba CLB/SLB (`2014-05-15`):

- [DescribeLoadBalancers](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describeloadbalancers)
- [DescribeLoadBalancerAttribute](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describeloadbalancerattribute)
- [DescribeVServerGroups](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describevservergroups)
- [DescribeVServerGroupAttribute](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describevservergroupattribute)
- [DescribeMasterSlaveServerGroups](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describemasterslaveservergroups)
- [DescribeMasterSlaveServerGroupAttribute](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describemasterslaveservergroupattribute)
- [Regional endpoints](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-endpoint) (Hangzhou uses `slb.aliyuncs.com`)

Alibaba ALB (`2020-06-16`):

- [ListLoadBalancers](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listloadbalancers)
- [ListListeners](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listlisteners)
- [ListRules](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listrules)
- [ListServerGroupServers](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listservergroupservers)
- [Regional endpoints](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-endpoint)

Alibaba instance references reuse
[ECS DescribeInstances](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances).
