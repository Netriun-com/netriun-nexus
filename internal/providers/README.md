# Provider adapters

T18 adds an unwired compute contract for T19. `internal/providers` has no store
or adapter imports. `Resource` mirrors the T17 columns; relations use the same
`Reference{Service, Region, Kind, ProviderID}` shape as the T17 store for both
ends, within the collection's account scope. Persistence resolves references
to database IDs and assigns `ID`, `Version`, `CreatedAt`, and `UpdatedAt`.
Adapters set account/workspace identity and one UTC `SeenAt` per successful list.

Each provider package exports `New(ClientFactory)`. The factory receives an
account identity (and region for AWS/Alibaba) and returns the existing cloud
client. The future caller resolves encrypted credentials and must bind the
client to that account. No credential loading, authorization, database writes,
or runtime registration happens here. Construct the adapters explicitly, then
pass a name-to-adapter map to `providers.NewRegistry` and use `Lookup`.

All adapters advertise `compute`, kind `instance`, with a five-minute default
interval. Existing scheduler configuration remains authoritative when T19 wires
the adapters in. AWS and Alibaba require a concrete `List` region. Azure and
GCP enumerate the subscription/project; an empty region or `*` returns all
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
currently has no request context). No `Apply`, non-compute kinds, sync, queue,
retention, HTTP, or persistence changes are included.

## Offline fixtures

Each adapter's `testdata` contains synthetic recorded-response fixtures shaped
from the official API examples, with documentation-range IPs and invented IDs.
Tests use only in-memory SDK/HTTP doubles, including OAuth token responses. GCP
signs with an ephemeral test key; no real credentials are used. Unexpected
requests fail closed. Azure/GCP tests temporarily replace the default transport
and therefore intentionally do not run in parallel within their package.

The shared parity test independently replays the same two pages through the
adapter and today's `internal/cloud` inventory function, compares all instance
fields/details, and checks pagination tokens. It also covers empty inventory,
a second-page access error (no partial results), Get, region filtering, account
identity validation, cancellation, and factory failures.

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
