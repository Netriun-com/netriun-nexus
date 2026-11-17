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

The application initializes the schema and bootstrap administrator on first startup. Subsequent boots do not reset passwords. Connect a cloud account in **Cloud connections**; opening a compute service shows its latest database snapshot immediately. Navigation and scope changes never queue a refresh; workers maintain freshness in the background.

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
- Background collection across selected or all enabled regions, independently of browser activity. Only Refresh live and post-action follow-ups request additional syncs; see [scheduled sync](#scheduled-sync).
- Paginated provider collection and transactional reconciliation per successful region; failed regions retain their previous inventory.
- Start, stop and reboot; live security group, volume and network interface details.
- Workspace-scoped users and a central policy engine with account-scoped Viewer, Operator, and Account Manager assignments.
- Workspace-scoped OIDC and SAML 2.0 single sign-on with JIT provisioning, claim/group-to-team mapping, and owner break-glass login.
- Valkey-backed sessions, local and federated logout, and login rate limiting. All inventory, EDS, OSS, and security-group refresh jobs and their status live in PostgreSQL.
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

### Resource catalog storage

Migration `016_resources.sql` adds the `resources` and `resource_relations`
tables and `internal/resources` store for phase 4. They are not connected to
collectors or HTTP routes yet; existing inventories and snapshots remain in use.

The migration runs `CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`.
The database server must provide the `pg_trgm` extension files. The migration
role needs permission to install the extension (including `CREATE` on the
database and the target schema), or a database administrator must install it
first. `pg_trgm` is a trusted extension since PostgreSQL 13; superuser access is
not required when the role has the necessary privileges. An existing extension
in another schema is reused. See the official [pg_trgm documentation](https://www.postgresql.org/docs/17/pgtrgm.html)
and [CREATE EXTENSION requirements](https://www.postgresql.org/docs/17/sql-createextension.html).

`resources.Reconcile` takes the caller's `pgx.Tx`, one workspace/account/service/
region/kind scope, and a fully successful collection. Future sync writers must
check their job lease in that transaction and roll it back on failure; the store
never opens or commits a transaction. It replaces only that scope's unseen
resources and outgoing relations. Missing relation targets are skipped until a
later collection discovers them. Account deletion cascades to resources and
relations. Only changes to name, state, tags, summary, or details increase a
resource's version; unchanged collections advance `seen_at` alone.

`List` requires a workspace and explicit accessible account IDs, returns summary
fields without details, and supports kind, service, region, state, and literal
substring `q` filters. `Get` applies the same access scope to the resource and
both ends of its relations. Lists sort by name and ID with an opaque cursor;
page sizes default to 50 and are capped at 200. A traversal retains its first
page's ID ceiling, so resources inserted between pages appear on the next
traversal instead. Filters and account access must remain the same. This is not
a transaction spanning pages: concurrent renames, deletions, or commits of
previously allocated IDs can change the result set.

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

Standalone workers require `DATABASE_URL` and `ENCRYPTION_KEY`; `REDIS_URL` is
optional and unused by the worker. They apply the same
transactional migrations without initializing HTTP sessions, SMTP, or the
bootstrap administrator; `serve`/`all` retain the existing application settings.
Scheduled collection runs in worker/all roles. Page navigation and scope changes
only read stored data. Helm defaults to separate serve and worker Deployments
(`worker.enabled=true`); set it to false for combined mode.

| Setting | Default | Purpose |
| --- | --- | --- |
| `NEXUS_WORKERS` | `8` | Maximum active handlers in each process |
| `NEXUS_ACCOUNT_CONCURRENCY` | `2` | Running jobs per `account:<id>` key across workers |
| `NEXUS_PROVIDER_CONCURRENCY` | `4` | Running jobs per `provider:<name>` key across workers |
| `NEXUS_SHUTDOWN_TIMEOUT` | `15s` | Time to drain handlers after SIGTERM/SIGINT |

Counts must be positive integers and the shutdown timeout a positive Go duration.
All workers sharing the queue must use the same per-key limits. Compute jobs
consume both `account:<id>` and `provider:<provider>` limits across replicas;
other jobs retain their existing single-key limits. Claim skips busy accounts
and providers, so one slow account does not occupy the entire pool. Other
nonempty keys default to one; empty keys are limited by the process pool only.

Workers poll every 500ms, lease jobs for 30s, and renew every 10s while a handler
runs. Only registered kinds are claimed. Successful handlers complete jobs;
errors and panics use the queue's bounded retries. Handlers must honor their
context, be safe to run again, and return errors without secrets. On SIGTERM,
claims stop immediately and lease renewal continues during the drain interval.
At its deadline, handlers are cancelled and renewal stops; unfinished jobs stay
durable and become claimable after lease expiry. A lost or uncertain lease also
cancels the handler. A crash may repeat work, so delivery is at least once.
Queue concurrency is shared by workers using the same PostgreSQL schema.
Independent schemas do not block each other’s claim keys.

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
| `NEXUS_SYNC_RECONCILE_INTERVAL` | `1m` | Database reconciliation, discovery due-time polling, and retention cleanup |
| `NEXUS_SYNC_DISCOVERY_INTERVAL` | `1h` | Provider region discovery per all-regions account |
| `NEXUS_AUDIT_RETENTION_INTERVAL` | `24h` | Per-workspace audit cleanup schedule |
| `NEXUS_JOB_RETENTION` | `168h` | Terminal job retention (7 days) |

Service lists show **Last synced** per account/region, plain-language errors, and
**Syncing…** for queued/running jobs. They poll the read-only
`GET /api/v1/sync-state` endpoint about every 30 seconds only while visible,
and patch changed rows, counts, and status without replacing filters, dialogs,
or scroll containers. Hidden tabs pause polling and check on return. EDS Users
also watches desktop sync completion for assignment counts. Data may appear up
to one polling interval after a job completes. Navigation, filters, scope changes,
and snapshot age never enqueue sync. Use Refresh live for an immediate targeted
request; successful actions keep their existing follow-ups.

AWS/Alibaba compute, EDS desktops, and security groups use per-region rows. Explicit selections
are reconciled in the account-save transaction; for “all regions,” the worker
discovery loop lists regions with the existing provider APIs. After upgrade,
maintenance also backfills existing accounts. Failed discovery preserves known
rows; new regional accounts need successful discovery before their first sync.
Discovery runs separately from database reconciliation and due-job polling.
Persisted per-account due times enforce the hourly cadence across restarts; a
PostgreSQL advisory lock allows only one replica to discover at a time. New
all-region accounts become eligible at the next reconciliation/discovery poll.
Failed discovery preserves scopes and retries after the configured interval.
Account deletion cascades rows;
region changes remove obsolete regional scopes. Disabled rows remain disabled.

The region sentinel `*` means a global API: Azure/GCP compute or Alibaba OSS
`ListBuckets`, which has no region filter. These rows retain their identities
when region selections change and never use the regional empty interval.
EDS users use directory endpoint scopes (`cn-shanghai` for mainland China,
`ap-southeast-1` for international regions, including Hong Kong). Multiple
desktop regions sharing a directory enqueue the same users job. Users jobs
collect only users; they no longer refresh desktops.

Reconciliation replaces legacy security-group and users `*` rows after their
regional scopes are known, preserving success history and old snapshots. It
reuses previously discovered compute/desktop scopes without waiting for the
next hourly discovery.
Users history is copied only to the directory actually represented by a stored
snapshot. For all-regions accounts, failed discovery retains legacy history
without scheduling the obsolete `*` scope. Regional snapshots supply the
existing all-regions desktop and security-group lists; EDS region counts come
from stored desktop snapshots.

Jobs carry workspace_id, account_id, service, region, and the state-row ID.
The key is `sync:<account_id>:<service>:<region>` and concurrency is
`account:<account_id>` plus `provider:<provider>`. Due-row locks plus transactional enqueue and active-key
uniqueness prevent duplicate jobs across replicas. Deleted/recreated scopes and
stale attempts cannot update newer scope metadata. HTTP job-status reads
authorize the payload's workspace_id: jobs has no workspace column.

Successful regional emptiness selects the six-hour interval; nonempty results
restore the service interval. Failures retain the last successful data, record
safe error text/code using a fresh three-second context, and use queue retries.
After attempts are exhausted the scope becomes due again at its service
interval. Known partial snapshots are failures. Cleanup removes at most 500
succeeded/failed jobs per maintenance pass based on their last update time;
queued/running jobs are retained regardless of age.

Compute collection now runs only as durable `scheduled.sync` jobs, including
manual refresh. `POST /api/v1/inventory/refresh` creates a PostgreSQL polling
receipt over the selected compute jobs, promotes queued jobs to priority 100,
and joins running jobs without duplication or bypassing retry backoff. An
optional `region` query parameter narrows AWS/Alibaba scopes; Azure/GCP retain
their account-wide `*` collector. If a selected account has no matching enabled
scope (including pending discovery), the request returns 409 without partially
enqueueing other accounts. Retrying after discovery queues the requested work.
The existing status URL and JSON shape remain compatible with the UI. Status
reads authorize both the receipt and every job's workspace/account. Receipts
survive server restarts and expire with terminal-job retention.

Compute writes check the current job lease and scope generation in the same
transaction as reconciliation, so an old attempt cannot overwrite newer data.
Successful compute jobs update `cloud_accounts.last_sync_at` to the oldest
successful timestamp across enabled compute rows (NULL while any has never
succeeded). Error summaries retain other regions' failures; credentials/access,
timeout, cancellation and provider errors use distinct safe messages, recorded
with a fresh context.

Audit cleanup runs independently of sync and refresh as a durable `audit.retention`
job per workspace, only in `worker` and `all` roles. A new workspace is due on the
next scheduler tick, even without cloud accounts. Each enqueue persists the next
due time using `NEXUS_AUDIT_RETENTION_INTERVAL` (default `24h`); changing the interval
affects the next enqueue, without resetting existing due times. Replicas share a
per-workspace dedupe and concurrency key. The handler reads
`settings.audit_retention_days` and deletes older rows in transactions of at most
500 rows, checking its job lease before committing each batch. Newer rows stay;
locked rows are left for the next run. Queue retries safely resume partial work.
The Helm setting is `sync.auditRetentionIntervalSeconds` (default `86400`).

EDS desktop/user, OSS, and security-group refresh endpoints also enqueue these
same durable jobs, promote queued jobs, join running jobs, and keep the existing
polling response shapes. A scope awaiting discovery returns 409. Every service
snapshot write checks the job lease and scope generation in its transaction.
Failed regional collections retain their previous rows. OSS commits only a
complete account snapshot: a failed bucket detail/statistics request retains
the previous snapshot and fails the job. Errors are classified and stored safely
with a fresh context; a failed row is never marked empty.

Stop pre-T14 workers/web processes before starting the upgraded release: old
binaries still contain unfenced Valkey runners. Old Valkey polling IDs are not
migrated; request a new refresh after upgrade. PostgreSQL queue receipts survive
subsequent restarts. Page sync triggers (T15) and audit retention (T16) are unchanged.

### HTTP operation

For a public deployment, terminate HTTPS at a reverse proxy, set `APP_ORIGIN` to the exact HTTPS origin, and set `COOKIE_SECURE=true`. Keep the application port private. `/healthz` checks the process; `/readyz` checks PostgreSQL and Valkey. Application logs go to stdout; Docker rotates them. Audit retention is independent of application log retention.

The application ignores forwarded client-IP headers by default. When running behind a reverse proxy, set `TRUSTED_PROXY_CIDRS` to the proxy address or network (comma-separated) so login rate limiting uses the original client IP. Trust only networks controlled by your deployment; for example, a local proxy can use `127.0.0.1/32`.

This release supports inventory and lifecycle operations for AWS EC2, Alibaba Cloud ECS, Azure Virtual Machines and Google Compute Engine; it does not provision or terminate those compute instances. Compute, Alibaba EDS desktops/users, OSS buckets, and security-group lists read PostgreSQL snapshots. Navigation, filters, and snapshot age never queue collection. Refresh buttons explicitly queue targeted syncs. Successful resource mutations request refreshes immediately and again after about 10, 30, and 90 seconds for the same scope; leaving the page or changing scope cancels the remaining requests. Action refreshes that meet an active job coalesce into one pending refresh after it finishes. Completed refreshes update the current table rows and status without replacing page controls, dialogs, or scroll containers; results for other scopes do not alter the screen. EDS Users consumes one all-regions desktop snapshot instead of issuing one browser request per region. OSS metadata and usage reads use bounded per-bucket concurrency; provider-reported usage can lag by more than one hour. Successful results replace the prior snapshot while provider failures retain the last healthy data. Equivalent jobs are coalesced, durable job leases fence snapshot writes, account/provider limits bound collection concurrency, inventory rows are batch-upserted, and Azure VM status reads are parallelized with a fixed limit. Operations that can create charges require explicit confirmation, and AutoPay defaults to off. AWS and Alibaba collection is regional; Azure and GCP use subscription/project-wide inventory endpoints with optional region filtering. Mutation intents must be stored before an action is submitted; other audit writes are best effort and failures are logged. Versioned SQL migrations run transactionally on startup. SAML supports SP-initiated login, optional IdP-initiated login, and signed Single Logout responses; OIDC uses discovery and RP-initiated logout when the provider publishes an end-session endpoint.

No production cloud action was performed during development. Real account validation requires your AWS/IAM or Alibaba Cloud RAM credentials and permissions.

## Kubernetes

A production-oriented Helm chart is available at [deploy/helm/netriun-nexus](deploy/helm/netriun-nexus). It deploys the application with non-root/read-only security settings and startup, liveness, and readiness probes. PostgreSQL and Valkey are external by default; the local profile can create retained single-node dependencies. Render and inspect the chart before deployment.

Implementation references: [AWS SDK for Go v2](https://docs.aws.amazon.com/sdk-for-go/), [Alibaba Cloud Go SDK V2](https://help.aliyun.com/en/sdk/developer-reference/use-alibaba-cloud-go-sdk-through-ide), [Alibaba OSS SDK for Go V2](https://github.com/aliyun/alibabacloud-oss-go-sdk-v2), [Azure Compute REST API](https://learn.microsoft.com/en-us/rest/api/compute/), [Google Compute Engine REST API](https://cloud.google.com/compute/docs/reference/rest/v1), [pgx](https://github.com/jackc/pgx/), [go-redis](https://redis.io/docs/latest/develop/clients/go/connect/).
