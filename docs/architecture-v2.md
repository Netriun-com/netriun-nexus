# Netriun Nexus — Target architecture (v2)

Status: approved by Saheb on 2026-10-30.
Source of truth while editing: [Target architecture in Claude Docs](https://claude.ai/artifact/QMTZGjDo57YL5FC1fiSShq).
This file is the repository copy that Codex reads. Change both together.

Related: [spec v2](spec-v2.md), [roadmap](roadmap.md), [task list](../task.md).

## Summary

Nexus v2 is a stateless API server plus a worker pool that share one PostgreSQL
database. The UI and CLI only read Nexus's own data, all provider calls run as
durable jobs, and changes reach the browser as a live event stream.

| Spec v2 requirement | Architecture decision |
| --- | --- |
| Pages open from Nexus data in under one second | All reads hit PostgreSQL; no provider call on the read path |
| Background sync with per-service intervals | A scheduler enqueues sync jobs per account, service, and region |
| No page refresh, no flicker | Server-Sent Events push row-level changes; the UI patches only those rows |
| One large account does not block others | Per-account and per-provider concurrency limits in the job queue |
| Restarts lose nothing | Jobs live in a PostgreSQL table claimed with `FOR UPDATE SKIP LOCKED` |
| One model for every provider | One `resources` table plus a `resource_relations` table for topology |
| Guided workflows with preview | An orchestration engine: intent, then plan, then approval, then step jobs |
| Two-person approval for large changes | An `approvals` record gates execution of risky plans |
| CLI covering the whole platform | The CLI is a client of the same public API, using personal API tokens |
| Licensing outside Nexus | Only the `entitlements` interface stays; it talks to the Netriun Commercial Platform |
| Path to EKS | API and worker scale horizontally; no in-process state |

## System overview

```text
 Browser (React UI) ──┐                       ┌──> AWS / Alibaba / Azure / GCP APIs
                      ├──> nexus serve (API) ──┤
 nexusctl (CLI) ──────┘        │  ▲           │
                               │  │ LISTEN    │
                               ▼  │           │
                          PostgreSQL ◄──── nexus worker (scheduler + jobs)
                          (resources, jobs,      NOTIFY
                           sync_state, …)
```

The browser and the CLI only talk to the API server, which only reads and
writes PostgreSQL. The worker is the only component that calls cloud providers;
its results reach the browser through PostgreSQL `NOTIFY` and the SSE stream.
API servers and workers can each run as several replicas. Valkey stays for
sessions and rate limiting.

## Data model

Six new tables replace today's split between the `instances` table and the
`service_snapshots` JSON blobs. Existing tables for workspaces, users, cloud
accounts, access policy, SSO, audit, and billing stay as they are.

| Table | One row per | Key columns | Purpose |
| --- | --- | --- | --- |
| `resources` | Provider object (instance, VPC, subnet, load balancer, security group, bucket, desktop, …) | `workspace_id`, `account_id`, `provider`, `service`, `kind`, `region`, `provider_id`, `name`, `state`, `tags`, `summary` (small JSONB), `details` (full JSONB), `seen_at`, `version` | The single catalog every list, search, topology, and CLI call reads |
| `resource_relations` | Edge between two resources | `from_id`, `to_id`, `relation` (`in_network`, `in_subnet`, `behind_lb`, `uses_rule`, …) | Topology view and safe-delete dependency checks |
| `sync_state` | Account + service + region | `interval`, `last_started_at`, `last_success_at`, `last_error`, `next_due_at` | Drives the scheduler and the "last synced" label in the UI |
| `jobs` | Unit of background work | `kind`, `payload`, `status`, `attempts`, `run_after`, `locked_by`, `locked_until`, `concurrency_key` | Durable queue for sync and orchestration steps |
| `operations` | One user intent (workflow run or single action) | `workflow`, `input`, `plan`, `status`, `requested_by`, `approval_id` | Groups the steps of one guided workflow and its audit trail |
| `approvals` | Approval request for a risky plan | `operation_id`, `requested_by`, `approver_id`, `decision`, `decided_at` | Two-person rule for large changes |

Design rules:

- Reconciliation stays as today: upsert what the provider returned, delete only
  rows of the same account, service, and region that were not seen, and only
  after a fully successful page set.
- Every write to `resources` bumps `version` and emits a change event (see
  "Live updates").
- Indexes: `(workspace_id, account_id, service, region)` for lists, a `pg_trgm`
  index on `name` and `provider_id` for search (replaces today's unindexed
  `ILIKE`), and `(from_id)`, `(to_id)` on relations.
- List APIs return `summary`, never `details`.

## Sync engine

Sync is driven by `sync_state`, not by page views: a scheduler loop in each
worker claims due rows and enqueues one job per account, service, and region.
Opening a page never enqueues anything.

**Scheduling.** Each service has a default interval that an administrator can
change. Defaults (approved; tune after measuring real accounts):

| Service class | Default interval | Reason |
| --- | --- | --- |
| Instances, desktops (state changes often) | 5 minutes | Users act on these |
| Networks, subnets, security groups, load balancers | 15 minutes | Change less often; workflows refresh them directly |
| Buckets, images, catalogs | 60 minutes | Rarely change |
| Billing | Daily | Provider data is itself delayed |
| Regions with no resources found | 6 hours | Avoids scanning empty regions every cycle |

**Targeted refresh.** After any Nexus operation, the worker refreshes only the
affected resource until it reaches a final state (for example `stopping` to
`stopped`), with backoff. The refresh button enqueues a high-priority job for
the one account, service, and region on screen; a second click while it is
queued does nothing.

**Fairness.** The queue enforces limits per `concurrency_key`: at most N
concurrent jobs per account and M per provider per worker, plus a global cap. A
large Alibaba account with many regions therefore runs in parallel with other
accounts instead of in front of them. Provider throttling errors back off that
account only.

**Why Alibaba is slow today, and what changes.** Today every page view scans
every region of every selected account in one request-scoped job with a
10-minute cap. In v2, empty regions move to the slow interval, each region is
its own short job with its own timeout and retry, and the UI never waits for
any of it. Exact Alibaba API behavior (page sizes, rate limits per API) must be
confirmed against Alibaba's documentation while implementing each adapter.

**Failures.** A failed job records `last_error` in `sync_state` using a fresh
context (today's bug of writing the error with an already-cancelled context
goes away), keeps the previous rows, and retries with exponential backoff. The
UI shows the error per account and region in plain language.

**Durability.** A job's lock has a lease (`locked_until`) that the worker
renews. If a worker dies, the lease expires and another worker picks the job
up. Graceful shutdown stops claiming new jobs and lets running ones finish or
release.

## Live updates to the UI

The browser opens one Server-Sent Events stream per session
(`GET /api/v1/events`) and receives small change events; it never polls job
status and never re-renders a whole page.

- **Event shape:** `{type, workspace_id, account_id, service, region,
  resource_id, version}` for resource changes, plus `sync.started`,
  `sync.finished`, `operation.step`, and `approval.requested`.
- **Fan-out (approved):** a worker that commits a change publishes to a
  PostgreSQL `NOTIFY` channel; each API replica `LISTEN`s and forwards events
  to its connected browsers, filtered by workspace and by the user's account
  access. Valkey pub/sub is the fallback if `NOTIFY` volume becomes a problem.
- **Client behavior:** the page holds a local store keyed by `resource_id`. On
  an event for a visible row it fetches that one resource (or uses the payload
  when it is enough) and patches the row in place. Events for other pages are
  ignored, so switching pages quickly cannot cancel or duplicate work.
- **Reconnect:** each event carries an ID; on reconnect the browser sends
  `Last-Event-ID`, and if the gap is too large it reloads the current list once.
- **Infrastructure note:** SSE is a long-lived HTTP response, so the server's
  `WriteTimeout` (60s today in `cmd/nexus/main.go`) must not apply to this
  route, and ingress or Cloudflare idle timeouts need a heartbeat comment every
  ~25 seconds.

## Orchestration engine

Every guided workflow and every single action (start, stop, delete) runs
through the same four-stage pipeline, stored as one `operations` row and a set
of step jobs.

1. **Intent:** the form input, validated against a typed workflow definition in
   Go (for example `network.create` with CIDR, subnet count, NAT, load
   balancer).
2. **Plan:** the workflow builds an ordered list of steps ("create VPC",
   "create VSwitch a", …) from the intent and the current `resources` catalog.
   It also runs checks such as CIDR overlap and quota hints. The plan is stored
   and shown as the preview; nothing has touched the provider yet.
3. **Approval:** a plan marked risky creates an `approvals` row and waits for a
   second user with the right role. The requester cannot approve their own
   plan. Low-risk plans skip this stage. Risky by default (approved): deletes,
   internet exposure, and security rule changes; adjustable per workspace.
4. **Execution:** each step is a job with a dependency on the previous ones.
   Steps record the provider ID they created so that a retry continues instead
   of duplicating. Where a provider API accepts an idempotency token (for
   example a `ClientToken` parameter), the step passes one; where it does not,
   the step looks up its own Nexus tag before creating. This must be checked
   per API while building each adapter.

**Partial failure.** When a step fails after retries, the operation stops in
`failed` with the completed steps listed. The user chooses: retry the failed
step, keep what exists, or roll back. Rollback runs the inverse steps in
reverse order and is itself a plan (previewed, and approved if risky).

**Audit.** The intent is recorded before the first provider call (as today),
and every step's start, result, and provider ID are appended to the same
operation's audit trail.

The first workflows built on this engine are the five approved in Spec v2. The
existing EDS wizard is migrated onto it first as the reference implementation,
because it already works end to end.

## Provider adapters

Each provider implements one Go interface, so the sync engine, the
orchestration engine, the API, and the CLI never switch on provider names.
Today the same `switch provider` block is repeated in `instances.go`,
`collector.go`, and elsewhere; that duplication goes away.

```go
type Provider interface {
    Services() []ServiceSpec                                     // kinds, regions, default interval
    List(ctx, acct, service, region) ([]Resource, []Relation, error)
    Get(ctx, acct, kind, id) (Resource, error)
    Apply(ctx, acct, step Step) (StepResult, error)              // create, update, delete, action
}
```

- **One package per provider:** `providers/aws`, `providers/alibaba`,
  `providers/azure`, `providers/gcp`, wrapping today's `internal/cloud` clients.
- **Normalization:** each adapter maps provider objects to the shared kinds
  (Network, Subnet, Instance, LoadBalancer, SecurityGroup, …) and keeps the
  provider's own name in `summary` so the UI can show both.
- **Scope per Spec v2:** Alibaba and AWS implement network kinds and `Apply`
  for the five workflows; Azure and GCP implement `List`, `Get`, and instance
  actions only.
- **Testing:** each adapter ships with recorded-response tests, so CI never
  needs real cloud credentials. The existing `internal/cloud` tests (63%
  coverage) are the starting point.

## API, CLI, and authentication

The UI and the CLI use the same versioned public API; nothing the UI can do is
UI-only.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/resources` | One list for every kind, with `kind`, `account_ids`, `region`, `state`, `q` filters and cursor pagination |
| `GET /api/v1/resources/{id}` | Details and relations of one resource |
| `GET /api/v1/topology` | Nodes and edges for one account and region |
| `POST /api/v1/operations` | Submit an intent (workflow or action); returns the plan |
| `POST /api/v1/operations/{id}/execute` | Run an approved or low-risk plan |
| `POST /api/v1/approvals/{id}` | Approve or reject |
| `POST /api/v1/sync` | Targeted refresh for one account, service, and region |
| `GET /api/v1/events` | Server-Sent Events stream |

Today's per-service endpoints (`/instances`, `/eds/...`, `/oss/...`) stay
during migration and are removed once the UI no longer calls them. Every new
endpoint is added to `docs/openapi.json` in the same PR.

**CLI (`nexusctl`, approved name):** a separate Go binary in `cli/` with
imperative, provider-neutral commands, for example
`nexusctl network create --account prod-ali --region cn-hangzhou --cidr 10.0.0.0/16 --subnets 2 --nat`,
`nexusctl instance stop <id>`, `nexusctl op approve <id>`. Every mutating
command shows the plan and asks for confirmation unless `--yes` is passed.
Output is a table by default and JSON with `--output json`.

**Authentication:** the API already accepts `Authorization: Bearer`, but only
with 12-hour browser session tokens. The CLI needs **personal API tokens**:
created and revoked in the UI, stored hashed, scoped to the user's existing
access policy, with an expiry date. No new permission model is needed; tokens
act as the user.

## Frontend

Decided: **React + TypeScript + Vite** (option A).

- New `web/` project; built assets embedded into the Go binary with `go:embed`
  as today; a graph library such as React Flow for topology. A Node build stage
  is added to the Dockerfile.
- The UI stays same-origin and keeps today's strict Content Security Policy
  (`script-src 'self'`), so no CDN scripts.
- The UI gets a generated, typed client from `docs/openapi.json`, so API and UI
  cannot drift apart.
- Screens are migrated one at a time; old and new screens can live side by side
  during the change.
- Library licenses must be checked against the AGPL boundary before adding
  them (see `docs/dependency-license-audit.md`).

## Code layout and licensing extraction

The monolithic `internal/app` package is split along the new components, one
move at a time, with no behavior change per move. One binary keeps two roles:
`nexus serve` (API and UI) and `nexus worker` (scheduler and jobs); a single
process can run both for small self-hosted installs. In Kubernetes they are two
Deployments (approved).

| Package | Holds | Comes from |
| --- | --- | --- |
| `internal/httpapi` | Routing, handlers, SSE, middleware | `internal/app` handlers |
| `internal/store` | SQL for every table; the only package that talks to PostgreSQL | SQL now inside handlers |
| `internal/jobs` | Queue, leases, scheduler, concurrency keys | `refresh_jobs.go`, `eds_refresh.go`, collector locks |
| `internal/sync` | Sync job handlers and reconciliation | `collector.go`, `snapshots.go` |
| `internal/orchestrator` | Workflows, plans, approvals, step execution | New; EDS wizard logic moves here |
| `internal/auth`, `internal/policy` | Sessions, SSO, API tokens, access policy | `auth.go`, `sso.go`, `policy.go`, `account_access.go` |
| `providers/{aws,alibaba,azure,gcp}` | Provider adapters | `internal/cloud` |
| `internal/entitlements` | The only licensing touchpoint left | Unchanged interface; new provider |
| `cli/` | `nexusctl` | New |

**Licensing extraction** (decided 2026-10-30): move `internal/licensing`,
`internal/enterprise`, `api/enterprise/v1`, the mTLS Compose example, the Helm
`enterprise-networkpolicy.yaml`, the `ENTERPRISE_*` settings, and the licensing
documents to the shared licensing project. In Nexus, `app.go` and
`installation.go` stop calling them, and `internal/entitlements` gets a
provider that will call the **Netriun Commercial Platform**. Its API is not
defined yet; until it is, Nexus runs with Community entitlements. Migrations
`009` and `010` stay in place (already applied); their tables simply stop being
used.

## Migration path from today's code

The change is incremental: every phase ships a working Nexus, and no phase
needs a big-bang rewrite. [roadmap.md](roadmap.md) tracks these phases and
[task.md](../task.md) holds the current tasks.

1. **Stabilize:** fix the HIGH CVEs, fix the README mismatches, add `AGENTS.md`
   for Codex, and work only through PRs. Hotfix the worst loading symptom in the
   current UI: stop starting a refresh on every navigation, and patch rows
   instead of re-rendering the page when a refresh finishes.
2. **Extract licensing:** remove licensing and the Enterprise client from the
   runtime, keep `internal/entitlements`.
3. **Durable jobs and sync:** add `jobs` and `sync_state`, the `nexus worker`
   role, and the scheduler; move today's four refresh systems onto it. Pages
   stop triggering sync.
4. **One resource model:** add `resources` and `resource_relations`, write to
   them from sync, move instances first, then EDS, OSS, and security groups off
   `service_snapshots`. Add `/api/v1/resources`.
5. **Live updates:** add the SSE stream and switch the UI to row patches.
6. **Frontend rebuild** (React + TypeScript + Vite): new UI on the typed API
   client, screen by screen.
7. **Orchestration and approvals:** engine, approvals, the EDS wizard as the
   reference workflow, then the five Spec v2 workflows on Alibaba and AWS, then
   topology.
8. **CLI and API tokens:** `nexusctl` on the same API.

Phases 1 and 2 can start immediately. Phases 3 to 5 fix the loading problem at
its root. The 2026-11-18 cluster migration does not change this order.
