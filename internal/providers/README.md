# Provider adapters

T22 adds the [regional AWS/Alibaba network service](NETWORK.md), including its
read-only API references, permissions and relation ownership rules.

T18 defines the compute contract; T19 uses it for scheduled and manual compute
sync. T20 adds Alibaba desktops, directory users, buckets and security groups.
`internal/providers` has no store or adapter imports. `Resource` mirrors the T17
columns; relations use the same
`Reference{Service, Region, Kind, ProviderID}` shape as the T17 store for both
ends, within the collection's account scope. Persistence resolves references
to database IDs and assigns `ID`, `Version`, `CreatedAt`, and `UpdatedAt`.
Adapters set account/workspace identity and one UTC `SeenAt` per successful list.

Each provider package exports `New(ClientFactory)` for compute. Alibaba also
exports `NewWithServices(ECSFactory, EDSFactory, OSSFactory)`, used by the worker,
to include all five services. The factory receives an
account identity (and region for AWS/Alibaba) and returns the existing cloud
client. The worker resolves encrypted credentials and binds the client to that
account. No credential loading, authorization, database writes,
or runtime registration happens here. Construct the adapters explicitly, then
pass a name-to-adapter map to `providers.NewRegistry` and use `Lookup`.

All adapters advertise `compute`, kind `instance`, with a five-minute default
interval. Existing scheduler configuration remains authoritative. AWS and Alibaba
require a concrete `List` region. Azure and GCP enumerate the subscription/project; an empty region or `*` returns all
regions, and a concrete region filters the completed inventory. `Account.Region`
selects the scope for `Get`. `Get` currently scans the same complete inventory
and returns the inventory representation or `ErrNotFound`; it does not fetch
expanded details. This can be expensive for large accounts.

| Shared field | Existing inventory value |
| --- | --- |
| `provider_id` | Instance ID; full ARM ID for Azure; `zone/name` for GCP |
| `name`, `state` | Existing name and normalized state |
| `region` | Requested region for AWS/Alibaba; inventory region for Azure/GCP |
| `tags` | Existing `details.tags` (GCP labels) |
| `summary.provider_type` | EC2 instance, ECS instance, Azure virtual machine, Compute Engine instance |
| `summary.instance_type` | Existing machine/instance type |
| `summary.public_ip`, `summary.private_ip` | Existing IP fields |
| `details` | Existing inventory details, including GCP's numeric `provider_id` |

Compute returns no relations. `List` returns no partial results after any page
error or cancellation. The wrapped collectors retain their existing behavior,
including SDK retries and Alibaba's context checks between pages (its SDK call
currently has no request context). The adapter packages handle no persistence,
queue, retention, or HTTP work and expose no `Apply`; the network wrapper
supplies the T22 kinds.

## Compute sync integration

`app.NewCollector` builds the registry once at worker startup with factories
wrapping the existing AWS, Alibaba, Azure and GCP client helpers. Credential
lookup remains scoped to workspace/account/provider and uses the encrypted
vault. The compute collector uses registry lookup, with no provider switch.

The scheduled handler carries its job attempt into `sync.ReconcileInventory`.
That transaction locks the account and checks the current lease, maps provider
resources to JSON-backed catalog items, preserves the existing `instances`
upsert/delete, calls `resources.Reconcile`, checks the lease again, then commits.
Any collection error, cancellation, catalog error or stale lease leaves both
inventories untouched. The legacy table remains the source for existing compute
pages; the resource catalog also receives the data. No read route changes are
part of T19, and no backfill runs at startup: each scope populates on its next
scheduled or manual sync.

AWS/Alibaba reconcile one region per job. Azure/GCP use an account-wide `*` job
but return each instance's real region. The existing configured-region filter
is applied before writing either table. Under the same account lock and lease,
`resources.ReconcileAllRegions` unions returned regions with previously stored
regions for this workspace/account/service/kind, then invokes `Reconcile` for
each real region. A formerly populated region absent from a complete result is
reconciled empty; a completely empty result clears all compute-instance regions.
Other accounts, workspaces, services and kinds are preserved. Partial or failed
results never reach reconciliation.

## Alibaba service sync integration

The worker's Alibaba adapter wraps the existing `internal/cloud` functions. It
preserves their page sizes and pagination and rejects the complete result on any
list/detail/stat error or cancellation. It collects EDS users alone; no desktop
refresh is a side effect. The scheduler's current interval settings stay
unchanged; advertised defaults are 5 minutes for desktops/users, 15 minutes for
security groups, and 60 minutes for buckets.

| Service | Kind | Resource region | Identity and mapping |
| --- | --- | --- | --- |
| `eds.desktops` | `desktop` | Requested region | `DesktopId`, `DesktopName`, lowercase `DesktopStatus`; desktop type, office site and charge type in summary; existing tags preserved |
| `eds.users` | `directory_user` | Directory endpoint region | `EndUserId`; real nickname or username; status 0 = normal, 9 = locked, other numeric values kept as text; email in summary |
| `oss.buckets` | `bucket` | Each bucket's location with the `oss-` prefix removed | Bucket name; available after info and stats succeed; storage class, storage bytes, object count and ACL in summary |
| `ecs.security_groups` | `security_group` | Requested region | Security group ID and name; available after all rules succeed; VPC, type and counts in summary; rules only in details |

EDS users map mainland desktop regions to `cn-shanghai` and other regions to
`ap-southeast-1` through the existing `cloud.EDSUserRegion` function. OSS `List`
requires `*` or an empty scope and enumerates the whole account; the factory uses
the account's first configured region (or `cn-hangzhou`) for that enumeration.
Bucket info and stats use each bucket's regional client. Configured regions do
not filter out buckets. `Get` scans the complete service inventory, using
`Account.Region` for regional services; no HTTP read route invokes it.

Each service resource's `details` is the exact existing collected item, including
EDS SDK field names, bucket statistics, and security-group rules. JSON numbers
retain int64 precision. The other three collectors do not return tags, so their
catalog tags are empty; this task adds no tag API calls or topology relations.

`app.collectService` calls the registry once per job and passes the complete
result to `sync.SaveServiceSnapshot`. That transaction locks the account, checks
the job lease and enabled sync-state generation, derives the legacy snapshot
from resource details, writes it, reconciles the catalog and checks the lease
again before commit. It retains `data`, `failed_regions` and `failed_buckets`
shapes. No separate transaction or best-effort second write is used. OSS uses
`resources.ReconcileAllRegions` to clear disappeared regions after a complete
successful account-wide collection. A failed bucket keeps the entire prior
snapshot and catalog; failed desktop/security-group regions keep their rows.

The previous fan-out limits remain: six concurrent bucket info/stat tasks and
eight security-group rule tasks per collection. Each rule task now obtains an
independent ECS client because the SDK mutates RPC headers during reads; sharing
one client across concurrent requests races. Factories must return independent
clients for concurrent detail tasks. Credentials remain in the worker's existing
workspace/account-scoped encrypted-vault lookup, outside the adapters.

## Offline fixtures

Each adapter's `testdata` contains synthetic recorded-response fixtures shaped
from the official API examples, with documentation-range IPs and invented IDs.
Tests use only in-memory SDK/HTTP doubles, including OAuth token responses. GCP
signs with an ephemeral test key; no real credentials are used. Unexpected
requests fail closed. Azure/GCP tests temporarily replace the default transport
and therefore intentionally do not run in parallel within their package.

The shared compute parity test independently replays the same two pages through the
adapter and today's `internal/cloud` inventory function, compares all instance
fields/details, and checks pagination tokens. It also covers empty inventory,
a second-page access error (no partial results), Get, region filtering, account
identity validation, cancellation, and factory failures.

Alibaba service fixtures in `alibaba/alibabatest/fixtures` are shared by adapter
and PostgreSQL scheduled-handler tests. They cover two list pages per service,
two rule pages per security group, bucket info/stats in two regions, continuation
errors, detail errors, cancellation and empty results. Unexpected requests fail
closed. PostgreSQL tests compare exact snapshot objects with catalog details,
exercise retries without version changes, and reject stale leases, including a
lease expiring after the snapshot write but before commit.

```sh
GOPROXY=off GOSUMDB=off go test -race ./internal/providers/...
```

## Official API references

These are the calls reused by the existing collectors:

- [AWS EC2 DescribeInstances](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstances.html): SDK paginator using `NextToken`.
- [Alibaba ECS DescribeInstances](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances): `MaxResults` and `NextToken`.
- [Azure Virtual Machines List All](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/list-all?view=rest-compute-2024-11-04): subscription list and `nextLink`; the page documents API version `2024-07-01`.
- [Azure Virtual Machines Instance View](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/instance-view?view=rest-compute-2024-11-04): runtime state, API version `2024-07-01`.
- [GCP instances.aggregatedList](https://docs.cloud.google.com/compute/docs/reference/rest/v1/instances/aggregatedList): project-wide list, `nextPageToken`, and existing `returnPartialSuccess=true` behavior.

Client construction uses the existing [Microsoft client credentials flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-client-creds-grant-flow)
and [Google service account OAuth flow](https://developers.google.com/identity/protocols/oauth2/service-account).

Alibaba service calls reused by T20 (no new permissions):

- [EDS DescribeDesktops](https://www.alibabacloud.com/help/en/wuying-workspace/developer-reference/api-ecd-2020-09-30-describedesktops): regional pages using `NextToken`.
- [EDS DescribeUsers](https://www.alibabacloud.com/help/en/wuying-workspace/developer-reference/api-eds-user-2021-03-08-describeusers-desktop) and [directory endpoints](https://www.alibabacloud.com/help/en/wuying-workspace/developer-reference/api-eds-user-2021-03-08-endpoint-desktop): token pagination and the Shanghai/Singapore directory regions.
- [OSS ListBuckets](https://www.alibabacloud.com/help/en/oss/developer-reference/listbuckets): account-wide enumeration with `Marker`/`NextMarker`.
- [OSS GetBucketInfo](https://www.alibabacloud.com/help/en/oss/developer-reference/getbucketinfo): per-bucket location and configuration.
- [OSS GetBucketStat](https://www.alibabacloud.com/help/en/oss/developer-reference/getbucketstat): storage/object counters; the provider notes that stats may lag by over an hour.
- [ECS DescribeSecurityGroups](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describesecuritygroups) and [DescribeSecurityGroupAttribute](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describesecuritygroupattribute): regional groups and paginated rules in both directions.
