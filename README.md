# Netriun Nexus

Netriun Nexus is a multi-cloud account orchestration platform for managing cloud resources, access, and infrastructure inventory in one place. It includes PostgreSQL, Valkey, an embedded web interface, and a versioned REST API. Each account receives an isolated workspace. AWS EC2, Alibaba Cloud ECS, WUYING EDS and OSS, Microsoft Azure Virtual Machines, and Google Compute Engine are supported.

The product direction and implementation sequence are documented in the
[product spec v2](docs/spec-v2.md), [target architecture](docs/architecture-v2.md),
and [roadmap](docs/roadmap.md). These distinguish current capabilities from
planned work.
Netriun Nexus uses an open-core model. The Community/Core program, public API,
CLI, and first-party provider implementations identified in [LICENSE.md](LICENSE.md)
are Open Source under **AGPL-3.0-only**. Netriun Enterprise and Netriun Cloud
implementations are separate proprietary boundaries and are not licensed by
the AGPL. See the [licensing architecture](docs/licensing-architecture.md) and
[dependency audit](docs/dependency-license-audit.md) before redistributing a
build. The AGPL does not grant rights to Netriun trademarks or logos.

## Run locally

Requirements: Docker Compose and OpenSSL.

```sh
sh scripts/setup-env.sh
docker compose up --build -d
```

Open http://localhost:8080. Sign in as `admin` with `ADMIN_PASSWORD` from `.env`, or create an independent Community workspace from the sign-up screen. The setup script generates independent secrets and refuses to overwrite an existing `.env`. Never commit this file. Back up `ENCRYPTION_KEY` alongside PostgreSQL backups: encrypted cloud credentials cannot be recovered without it.

The application initializes the schema and bootstrap administrator on first startup. Subsequent boots do not reset passwords. Connect a cloud account in **Cloud connections**; opening a compute service shows its latest database snapshot immediately and queues a live provider refresh only when the snapshot is missing or older than five minutes and no refresh for that scope is active.

`DEPLOYMENT_MODE` is explicit and accepts `self_hosted` or `cloud`; local,
Compose, and the bundled Helm values default to `self_hosted`. Authenticated
clients can read the sanitized edition, feature decisions, and current
workspace limit usage from `GET /api/v1/entitlements`. Product limits and
premium capability decisions are enforced separately from workspace RBAC.

All installations currently use Community entitlements while licensing moves to
the external Netriun Commercial Platform. Nexus no longer loads offline licenses
or connects to the Enterprise service; old `ENTERPRISE_*` and
`SPIFFE_ENDPOINT_SOCKET` settings are ignored. Administrator-only
`GET /api/v1/installation` returns `installation_id`, `deployment_mode`,
`edition`, and `license_status`; the edition and license status are `community`.

The sidebar keeps cloud scope explicit: select one active provider, then select one or more accounts from that provider. Cloud services are loaded for the active provider and grouped by capability. Workspace administration and product documentation—including the API reference—have separate navigation sections. Multi-account inventory API calls use a comma-separated `account_ids` query value; selected IDs must be accessible and belong to the same provider.

```sh
docker compose logs -f app
docker compose down
```

If port 8080 is occupied, set `PORT` and the matching `APP_ORIGIN` in `.env` (for example, `PORT=18080` and `APP_ORIGIN=http://localhost:18080`).

Persistent volumes survive `down`. The portal, PostgreSQL and Valkey are published on loopback only; they are not reachable through external network interfaces. Database ports are exposed locally to support host-based Go development.

When upgrading an existing installation from Netriun CCMP, stop the old application and database cleanly before copying its PostgreSQL and Redis volumes to the Nexus volume names. Never copy a live PostgreSQL data directory. Keep the old volumes as a rollback/forensic backup until the Nexus installation has been verified.

## Features

- Public Community workspace creation with strict tenant isolation.
- One owner plus five additional team members per Community workspace.
- Searchable, paginated compute inventory across AWS, Alibaba Cloud, Azure and Google Cloud.
- An active-account context that exposes only the services belonging to the selected cloud provider.
- Live Alibaba WUYING EDS desktops and convenience users, including create, renew, user assignment, start, stop, and reboot workflows.
- Snapshot-first Alibaba OSS bucket inventory with metadata, usage statistics, search/sort/filter, list/visual modes, and private bucket creation.
- Snapshot-first Alibaba ECS security groups with attached-instance visibility, list/visual modes, rule inspection, and audited ingress/egress rule creation and deletion.
- Guided AWS, Alibaba Cloud, Azure and Google Cloud connection setup with a required live credential and compute-access test before saving.
- Collection across explicitly selected or all enabled regions when a service page opens a missing or older-than-five-minute snapshot, or an explicit refresh or user action requests it. A durable worker scheduler also refreshes scopes without a browser; see [scheduled sync](#scheduled-sync).
- Paginated provider collection and transactional reconciliation per successful region; failed regions retain their previous inventory.
- Start, stop and reboot; live security group, volume and network interface details.
- Workspace-scoped users and a central policy engine with account-scoped Viewer, Operator, and Account Manager assignments.
- Workspace-scoped OIDC and SAML 2.0 single sign-on with JIT provisioning, claim/group-to-team mapping, and owner break-glass login.
- Valkey-backed sessions, local and federated logout, login rate limiting, asynchronous refresh-job status, and per-account live-refresh leases.
- AES-256-GCM encryption for cloud credentials, bcrypt passwords and same-origin mutation checks.
- Audit history, retention settings, structured application logs and container log rotation.
- Searchable in-app Documentation Center and contextual Help, backed by one structured content source and the stable [documentation map](docs/index.md).
- REST API at `/api/v1`, in-app API reference and [OpenAPI specification](docs/openapi.json).
- Offline [legacy migration](docs/migration.md), with a dry-run default.
- Netriun visual design with persistent dark, light and system appearance modes.
- AWS, Alibaba Cloud, Microsoft Azure and Google Cloud compute inventory and lifecycle actions.

## Permissions

Every workspace is isolated at the database-query and authorization layers. The person who creates it is its permanent owner. Workspace administrators can manage that workspace only. Users see only accounts in groups with `view_dashboard` enabled. Accounts with no group are administrator-only.

| Group role | Capabilities |
| --- | --- |
| viewer | Inventory and live details when view policy is enabled |
| operator | Viewer capabilities plus start, stop and reboot |
| manager | Operator capabilities; account and membership management when the corresponding group policies allow it |

Only workspace administrators edit group policies, users, audit retention settings, and view that workspace's audit records. Moving an account requires management permission on both the current and destination groups. Password or user-role updates revoke all that user's sessions. Owners cannot be removed or demoted.

## API example

```sh
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"YOUR_PASSWORD"}'

curl http://localhost:8080/api/v1/instances?state=running \
  -H 'Authorization: Bearer YOUR_TOKEN'
```

Create a free isolated workspace with `POST /api/v1/auth/signup` and `{"workspace":"Team name","username":"owner","email":"owner@example.com","password":"Strong-password1!"}`. Email addresses and usernames are globally unique. A 24-hour verification link is sent by email, and sign-in remains disabled until it is used.

Login uses the verified email address and returns a token valid for 12 hours plus an HttpOnly session cookie. Passwords must be 10–72 bytes and contain uppercase, lowercase, a number, and a symbol. Lists use `{"data": [...]}`. Errors use `{"error":"message"}`. Inventory and audit lists support `limit` (default 50, maximum 200) and `offset`.

Workspace administrators configure OIDC or SAML providers under **Workspace settings**. OIDC uses Authorization Code flow with discovery, state, nonce, and PKCE S256. SAML uses a generated per-provider SP key pair, HTTP-Redirect AuthnRequest, HTTP-POST ACS, signed response validation, request correlation, and persistent NameID. Group claims map to Nexus teams without overwriting manual memberships. See the [SSO guide](docs/sso.md).

## AWS access

Use dedicated AWS credentials. Inventory needs `ec2:DescribeRegions` and `ec2:DescribeInstances`. Live details also need `ec2:DescribeSecurityGroups`, `ec2:DescribeVolumes` and `ec2:DescribeNetworkInterfaces`. Actions need `ec2:StartInstances`, `ec2:StopInstances`, and `ec2:RebootInstances` for permitted instances. A policy example is in [docs/aws-policy.json](docs/aws-policy.json); restrict its action resources to your environment before use. Temporary session tokens are supported but must be replaced before expiry.

## Alibaba Cloud access

Use a dedicated RAM user AccessKey, not a root-account AccessKey. ECS inventory needs `ecs:DescribeRegions` and `ecs:DescribeInstances`; actions need `ecs:StartInstance`, `ecs:StopInstance`, and `ecs:RebootInstance`. Security-group inventory needs `ecs:DescribeSecurityGroups` and `ecs:DescribeSecurityGroupAttribute`; its separate Authorize/Revoke statement is needed only for rule management. WUYING EDS uses narrowly listed `ecd` actions for region discovery, desktop inventory, provisioning, renewal, lifecycle, policy, maintenance, remote-command, billing, entitlement, and convenience-user management. OSS inventory needs `oss:ListBuckets`, `oss:GetBucketInfo`, and `oss:GetBucketStat`; private bucket creation additionally needs `oss:PutBucket`. Follow the [Alibaba Cloud connection guide](docs/alibaba.md) and start from [docs/alibaba-policy.json](docs/alibaba-policy.json); remove product statements you do not use and restrict ECS action resources before production use. Static STS security tokens are supported and must be replaced before expiry. Leaving regions blank discovers all ECS regions visible to the credential.

## Azure and Google Cloud access

Azure uses a Microsoft Entra service principal scoped to a subscription. Google Cloud uses a dedicated service-account JSON key scoped to a project. Nexus discovers Azure Virtual Machines and Google Compute Engine instances, provides live details, and supports start, stop and reboot/reset when the identity has the required role. Follow the [Azure and Google Cloud connection guide](docs/azure-gcp.md) and prefer read-only roles when lifecycle actions are not needed.

## Product documentation

The portal includes a searchable Documentation page and a contextual **Help** button that opens the article relevant to the current page. Both read from `internal/app/web/docs.js`, so guidance stays consistent. The long-term structure is fixed as **Getting Started → Cloud Connections → Services → Access Management → Operations → Troubleshooting**; use the [documentation map](docs/index.md) when expanding it in later phases.

## License

The Open Source Work is licensed under `AGPL-3.0-only`; its exact path scope
and exclusions are defined in [LICENSE.md](LICENSE.md), with the full license
text in [LICENSE](LICENSE). Third-party components retain their own licenses
and notices. Documentation licensing, the Enterprise commercial license, and
the trademark policy remain separate decisions and are not implied by the
software license.

## Go development

Go version is declared in `go.mod`. `make run` loads `.env`, starts PostgreSQL and Valkey in containers, stops the Compose application service to prevent duplicate local application processes, and runs the Go process on `http://localhost:8080`. It derives host-reachable database URLs from the Compose passwords, using local ports `15432` and `16379` by default. The `REDIS_URL` name remains for protocol compatibility.

Email verification uses SMTP with mandatory STARTTLS. Set `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM_ADDRESS`, and `SMTP_FROM_NAME`; use an app password rather than a primary mailbox password.

```sh
make run
go test -race ./...
go vet ./...
go build -o bin/nexus ./cmd/nexus
```

If a default local port is occupied, set `LOCAL_PORT`, `POSTGRES_PORT`, or `REDIS_PORT` in `.env`. Advanced setups can provide `RUN_DATABASE_URL` and `RUN_REDIS_URL` to make the host process use external dependencies.

Integration tests run when `TEST_DATABASE_URL` and `TEST_REDIS_URL` are provided. **Use isolated databases**: tests truncate application tables and flush the selected Valkey logical database. PostgreSQL database name must contain `ccmp_test`. CI provisions these dependencies.

## Operation and limitations

### Process roles and durable workers

`NEXUS_ROLE` accepts `serve`, `worker`, or `all` (the default). Invalid roles
fail startup. `serve` runs the existing HTTP application with `/readyz` checking
PostgreSQL and Valkey. `worker` runs only the durable job runtime and a small
`GET /healthz` listener on `HTTP_ADDR` (default `:8080`); it does not expose the
application API or `/readyz`. `all` runs both in one process. Compose explicitly
keeps one `all` container. Use different HTTP_ADDR ports when running separate
processes on the same host.

```sh
NEXUS_ROLE=serve HTTP_ADDR=:8080 ./bin/nexus
NEXUS_ROLE=worker HTTP_ADDR=:8081 ./bin/nexus
```

Standalone workers require `DATABASE_URL`, `REDIS_URL`, and `ENCRYPTION_KEY`
for scheduled collectors and their existing Valkey locks. They apply the same
transactional migrations without initializing HTTP sessions, SMTP, or the
bootstrap administrator; `serve`/`all` retain the existing application settings.
Scheduled collection runs in worker/all roles. Existing page/button refresh
systems continue to run as before.

| Setting | Default | Purpose |
| --- | --- | --- |
| `NEXUS_WORKERS` | `8` | Maximum active handlers in each process |
| `NEXUS_ACCOUNT_CONCURRENCY` | `2` | Running jobs per `account:<id>` key across workers |
| `NEXUS_PROVIDER_CONCURRENCY` | `4` | Running jobs per `provider:<name>` key across workers |
| `NEXUS_SHUTDOWN_TIMEOUT` | `15s` | Time to drain handlers after SIGTERM/SIGINT |

Counts must be positive integers and the shutdown timeout a positive Go duration.
All workers sharing the queue must use the same per-key limits. Each job has one
concurrency key: account and provider keys are separate scopes, not simultaneous
hierarchical limits on one job. Other nonempty keys retain the queue's default
limit of one; empty keys are limited by the process pool only.

Workers poll every 500ms, lease jobs for 30s, and renew every 10s while a handler
runs. Only registered kinds are claimed. Successful handlers complete jobs;
errors and panics use the queue's bounded retries. Handlers must honor their
context, be safe to run again, and return errors without secrets. On SIGTERM,
claims stop immediately and lease renewal continues during the drain interval.
At its deadline, handlers are cancelled and renewal stops; unfinished jobs stay
durable and become claimable after lease expiry. A lost or uncertain lease also
cancels the handler. A crash may repeat work, so delivery is at least once.

Worker `/healthz` reports runtime liveness (200 while active, 503 while stopping),
not database/provider readiness. Kubernetes should probe it on the worker pod's
HTTP port, without a public Service. Set termination grace to at least
`max(15s, NEXUS_SHUTDOWN_TIMEOUT) + 5s`; the HTTP server has its own 15s shutdown
budget, running concurrently with worker drain. See the
[Helm chart README](deploy/helm/netriun-nexus/README.md) for separate Deployments.

### Scheduled sync

Workers maintain `sync_state` and enqueue durable `scheduled.sync` jobs without
any browser activity. Defaults are configurable as Go durations of at least 1s;
use the same values in all processes. Compose passes these settings through.

| Environment variable | Default | Scope |
| --- | --- | --- |
| `NEXUS_SYNC_COMPUTE_INTERVAL` | `5m` | Compute inventory |
| `NEXUS_SYNC_DESKTOPS_INTERVAL` | `5m` | EDS desktops |
| `NEXUS_SYNC_USERS_INTERVAL` | `5m` | EDS users |
| `NEXUS_SYNC_SECURITY_GROUPS_INTERVAL` | `15m` | Security groups |
| `NEXUS_SYNC_BUCKETS_INTERVAL` | `1h` | OSS buckets |
| `NEXUS_SYNC_EMPTY_INTERVAL` | `6h` | Successful empty regional scopes only |
| `NEXUS_SYNC_TICK_INTERVAL` | `10s` | Due-job polling, up to 100 scopes per tick |
| `NEXUS_SYNC_RECONCILE_INTERVAL` | `1m` | Account reconciliation and retention cleanup |
| `NEXUS_JOB_RETENTION` | `168h` | Terminal job retention (7 days) |

AWS/Alibaba compute and EDS desktops use per-region rows. Explicit selections
are reconciled in the account-save transaction; for “all regions,” worker
maintenance discovers regions with the existing provider APIs. After upgrade,
maintenance also backfills existing accounts. Failed discovery preserves known
rows; new regional accounts need successful discovery before their first sync.
Discovery runs separately from due-job polling. Account deletion cascades rows;
region changes remove obsolete regional scopes. Disabled rows remain disabled.

The region sentinel `*` means a whole-account collector: Azure/GCP compute,
Alibaba OSS, security groups, and EDS users. These rows retain their identities
when region selections change, keep their service interval even when empty, and
never set the regional `empty` flag. TODO(T14): replace Alibaba `*` rows when
collectors become regional. EDS users may refresh desktop snapshots as an
existing side effect; only its own users row records that job's outcome.

Jobs carry workspace_id, account_id, service, region, and the state-row ID.
The key is `sync:<account_id>:<service>:<region>` and concurrency is
`account:<account_id>`. Due-row locks plus transactional enqueue and active-key
uniqueness prevent duplicate jobs across replicas. Deleted/recreated scopes and
stale attempts cannot update newer scope metadata. Future HTTP job-status reads
(T13) must authorize the payload's workspace_id: jobs has no workspace column.

Successful regional emptiness selects the six-hour interval; nonempty results
restore the service interval. Failures retain the last successful data, record
safe error text/code using a fresh three-second context, and use queue retries.
After attempts are exhausted the scope becomes due again at its service
interval. Known partial snapshots are failures. Cleanup removes at most 500
succeeded/failed jobs per maintenance pass based on their last update time;
queued/running jobs are retained regardless of age.

Collectors and their Valkey coordination remain unchanged. The current UI and
manual HTTP refresh runners are not moved to the durable queue in this task.
Regional desktop sync does not rebuild the all-region desktop snapshot; current
EDS users/manual collectors still do that. Snapshot freshness shown by the
legacy UI is not yet driven by sync_state (T13–T15).

### HTTP operation

For a public deployment, terminate HTTPS at a reverse proxy, set `APP_ORIGIN` to the exact HTTPS origin, and set `COOKIE_SECURE=true`. Keep the application port private. `/healthz` checks the process; `/readyz` checks PostgreSQL and Valkey. Application logs go to stdout; Docker rotates them. Audit retention is independent of application log retention.

The application ignores forwarded client-IP headers by default. When running behind a reverse proxy, set `TRUSTED_PROXY_CIDRS` to the proxy address or network (comma-separated) so login rate limiting uses the original client IP. Trust only networks controlled by your deployment; for example, a local proxy can use `127.0.0.1/32`.

This release supports inventory and lifecycle operations for AWS EC2, Alibaba Cloud ECS, Azure Virtual Machines and Google Compute Engine; it does not provision or terminate those compute instances. Every compute, Alibaba EDS, and Alibaba OSS listing page reads the latest PostgreSQL snapshot first and queues live collection asynchronously only if that scope is missing or older than five minutes and no refresh for it is queued or running. Refresh buttons bypass the age check. Successful resource mutations request refreshes immediately and again after about 10, 30, and 90 seconds for the same scope; leaving the page or changing scope cancels the remaining requests. Action refreshes that meet an active job coalesce into one pending refresh after it finishes. Completed refreshes update the current table rows and status without replacing page controls, dialogs, or scroll containers; results for other scopes do not alter the screen. EDS Users consumes one all-regions desktop snapshot instead of issuing one browser request per region. OSS metadata and usage reads use bounded per-bucket concurrency; provider-reported usage can lag by more than one hour. Successful results replace the prior snapshot while provider failures retain the last healthy data. Equivalent jobs are coalesced, account/service leases prevent overlapping collection, accounts and regions use bounded concurrency, inventory rows are batch-upserted, and Azure VM status reads are parallelized with a fixed limit. Operations that can create charges require explicit confirmation, and AutoPay defaults to off. AWS and Alibaba collection is regional; Azure and GCP use subscription/project-wide inventory endpoints with optional region filtering. Mutation intents must be stored before an action is submitted; other audit writes are best effort and failures are logged. Versioned SQL migrations run transactionally on startup. SAML supports SP-initiated login, optional IdP-initiated login, and signed Single Logout responses; OIDC uses discovery and RP-initiated logout when the provider publishes an end-session endpoint.

No production cloud action was performed during development. Real account validation requires your AWS/IAM or Alibaba Cloud RAM credentials and permissions.

## Kubernetes

A production-oriented Helm chart is available at [deploy/helm/netriun-nexus](deploy/helm/netriun-nexus). It deploys the application with non-root/read-only security settings and startup, liveness, and readiness probes. PostgreSQL and Valkey are external by default; the local profile can create retained single-node dependencies. Render and inspect the chart before deployment.

Implementation references: [AWS SDK for Go v2](https://docs.aws.amazon.com/sdk-for-go/), [Alibaba Cloud Go SDK V2](https://help.aliyun.com/en/sdk/developer-reference/use-alibaba-cloud-go-sdk-through-ide), [Alibaba OSS SDK for Go V2](https://github.com/aliyun/alibabacloud-oss-go-sdk-v2), [Azure Compute REST API](https://learn.microsoft.com/en-us/rest/api/compute/), [Google Compute Engine REST API](https://cloud.google.com/compute/docs/reference/rest/v1), [pgx](https://github.com/jackc/pgx/), [go-redis](https://redis.io/docs/latest/develop/clients/go/connect/).
