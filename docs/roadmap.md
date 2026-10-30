# Netriun Nexus roadmap (v2)

Status: approved plan, started 2026-10-30.
Inputs: [spec v2](spec-v2.md) and [target architecture](architecture-v2.md).
Current tasks: [task.md](../task.md).
The previous milestone plan (M0 to M10) is archived in
[archive/roadmap-v1.md](archive/roadmap-v1.md).

## How to use this roadmap

- Phases run in order. A phase starts only when the previous phase's exit
  criteria are met, except where a phase says it can run in parallel.
- Every phase ships a working Nexus. There is no big-bang rewrite.
- `task.md` holds the tasks for the current phase only. When a phase is done,
  its tasks are removed from `task.md`, this file is updated, and the next
  phase's tasks are written and reviewed before work starts.
- Codex implements. Saheb and Claude review every change through a pull
  request. Nothing is pushed directly to `main`.
- The cluster migration on 2026-11-18 moves whatever state exists that day. It
  does not change this order.

## Phase overview

| Phase | Goal | Fixes | Status |
| --- | --- | --- | --- |
| 1. Stabilize | Releasable image, honest docs, safe Codex workflow, loading hotfix | CVEs block releases; page flicker | **Current** |
| 2. Extract licensing | Licensing leaves the runtime; entitlements ready for the Netriun Commercial Platform | Licensing coupling | Can run in parallel with phase 1 |
| 3. Durable jobs and sync | Background scheduler, durable job queue, `nexus worker` role | Sync on page view; lost jobs; one account blocks others | Next |
| 4. One resource model | `resources` + `resource_relations`, `/api/v1/resources` | Two data models; unindexed search | Planned |
| 5. Live updates | SSE stream, row patches | Full page re-render | Planned |
| 6. Frontend rebuild | React + TypeScript + Vite UI on a typed client | 167KB single-file UI | Planned |
| 7. Orchestration and approvals | Engine, approvals, the five workflows, topology | Product vision | Planned |
| 8. CLI and API tokens | `nexusctl` and personal API tokens | CLI capability | Planned |

Phases 3 to 5 fix the loading problem at its root. Phases 6 to 8 deliver the
v2 product. The first release (MVP) is the end of phase 8.

## Phase 1 — Stabilize

- [ ] The `Images` workflow is green again: no HIGH or CRITICAL findings in the
  built image.
- [ ] `AGENTS.md` exists and states the architecture rules, PR size, test
  requirements, and the "code and docs change together" rule.
- [ ] `main` accepts changes only through reviewed pull requests.
- [ ] The README and docs no longer claim features that do not exist
  (scheduled collection, approvals) and point to spec v2.
- [ ] Hotfix: navigating between pages no longer starts a live refresh for a
  fresh snapshot, and a finished refresh no longer re-renders the whole page.

Exit: a signed image can be released, and switching pages quickly in the
current UI causes no flicker and no duplicate refresh jobs.

## Phase 2 — Extract licensing

- [ ] `internal/licensing`, `internal/enterprise`, and `api/enterprise/v1` are
  no longer linked into the Nexus binary.
- [ ] `internal/entitlements` keeps its interface and gains a
  `commercialplatform` provider stub for the Netriun Commercial Platform that
  returns Community until that platform's API exists.
- [ ] `ENTERPRISE_*` settings, the mTLS Compose example, the Helm
  `enterprise-networkpolicy.yaml`, and the licensing documents are removed from
  this repository (archived in the shared licensing project).
- [ ] Migrations `009` and `010` stay unchanged.

Exit: Nexus builds and runs with Community entitlements and no licensing code;
existing installations upgrade without data loss.

## Phase 3 — Durable jobs and sync

- [ ] `jobs` table with leases, retries, backoff, and per-account and
  per-provider concurrency keys.
- [ ] `sync_state` table and a scheduler with the approved default intervals.
- [ ] `nexus serve`, `nexus worker`, and combined mode in one binary; Helm runs
  two Deployments.
- [ ] Today's four refresh systems (inventory, EDS, OSS, security groups) run
  on the queue. Pages never trigger sync; the refresh button enqueues one
  targeted job.
- [ ] Sync errors are written with a fresh context and shown per account and
  region.
- [ ] Audit retention runs as a scheduled job.

Exit: killing a worker mid-sync loses no work, and a large Alibaba account does
not delay other accounts.

## Phase 4 — One resource model

- [ ] `resources` and `resource_relations` tables with the indexes in the
  architecture (including `pg_trgm`).
- [ ] Sync writes instances, then EDS, OSS, and security groups, into
  `resources`; `service_snapshots` is no longer written.
- [ ] Alibaba and AWS sync networks, subnets, security groups, and load
  balancers with relations.
- [ ] `GET /api/v1/resources` and `GET /api/v1/resources/{id}` with cursor
  pagination, documented in `docs/openapi.json`.
- [ ] Provider adapters behind the `Provider` interface in
  `providers/{aws,alibaba,azure,gcp}` with recorded-response tests.

Exit: every list in the UI reads from `resources` in under one second.

## Phase 5 — Live updates

- [ ] `GET /api/v1/events` (SSE) with `LISTEN/NOTIFY` fan-out, access filtering,
  `Last-Event-ID`, and heartbeats.
- [ ] No `WriteTimeout` on the SSE route; ingress and Cloudflare settings
  documented.
- [ ] The UI patches changed rows and no longer polls job status.

Exit: a resource change appears in an open browser without a page refresh or
flicker.

## Phase 6 — Frontend rebuild

- [ ] `web/` project (React + TypeScript + Vite) embedded with `go:embed`;
  strict CSP kept.
- [ ] Typed API client generated from `docs/openapi.json`.
- [ ] Screens migrated one at a time; the old `app.js` is deleted at the end.
- [ ] Dependency licenses checked against the AGPL boundary.

Exit: every current screen runs on the new frontend with tests.

## Phase 7 — Orchestration and approvals

- [ ] `operations` and `approvals` tables, the orchestration engine, and the
  operations API.
- [ ] The EDS wizard migrated onto the engine as the reference workflow.
- [ ] The five spec v2 workflows on Alibaba and AWS.
- [ ] Two-person approval for risky plans (deletes, internet exposure, security
  rule changes; adjustable per workspace).
- [ ] Topology view.

Exit: a user builds a network on Alibaba and on AWS from one form, with preview,
live progress, and one audit record.

## Phase 8 — CLI and API tokens

- [ ] Personal API tokens (create, revoke, expiry, stored hashed).
- [ ] `nexusctl` covering resources, workflows, operations, and approvals, with
  table and JSON output.

Exit: everything the UI can do can be done with `nexusctl`.

## After the first release

- Database management (RDS, ApsaraDB, …).
- Community versus Enterprise feature split, decided after the first workflows
  exist.
- Public SaaS (Nexus Cloud), revisited after the MVP.
- Cross-cloud connectivity and Infrastructure-as-Code export.
