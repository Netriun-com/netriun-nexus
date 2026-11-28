# Current tasks

Phase: **5 — Live updates** (see [docs/roadmap.md](docs/roadmap.md)). Phase 2 still has T8 open.
Written: 2026-10-30 (phases 1–2), 2026-11-09 (phase 3), 2026-11-16 (phase 4), 2026-11-28 (phase 5).

Read first: [docs/spec-v2.md](docs/spec-v2.md) and
[docs/architecture-v2.md](docs/architecture-v2.md).

## Rules for every task

- One task = one pull request against `main`. Never push to `main` directly.
- Keep the PR to the task's scope. If you find other problems, list them in the
  PR description instead of fixing them.
- Before opening the PR, run and pass: `go build ./...`, `go vet ./...`,
  `go test -race ./...`, and the integration tests with PostgreSQL and Valkey
  (as in `.github/workflows/ci.yml`).
- Update `docs/openapi.json` in the same PR whenever an HTTP route, request, or
  response changes.
- Update docs in the same PR as the code they describe.
- Do not guess provider API behavior. Check the provider's official
  documentation and link it in the PR description.
- If a task's acceptance criteria cannot be met, stop and explain why in the
  PR description. Do not weaken a test or skip a check to make CI pass.
- When a task is merged, tick it here in the same PR or a follow-up.

## Starting point

Phases 1, 3, and 4 are done. Phase 5 starts with **T24** and **T27** in parallel
(see the order table in phase 5). T8 waits for Saheb's confirmation.

---

## Phase 1 — Stabilize

### T1. Fix the HIGH CVEs so the Images workflow is green

- [x] Done — merged in [PR #2](https://github.com/Netriun-com/netriun-nexus/pull/2).

Why: the `Images` workflow (`.github/workflows/images.yml`, step
`make image-scan`) has failed daily since 2026-10-25 with 11 HIGH findings, so
no release is possible.

Known findings at `0929f6a` (re-check the latest failing run's log first):

| Module | Version | Notes |
| --- | --- | --- |
| `github.com/go-jose/go-jose/v4` | v4.1.5 (indirect) | Fixed in v4.1.6 |
| `golang.org/x/net` | v0.58.0 (indirect) | Fixed in v0.60.0 or later |
| `github.com/xuri/excelize/v2` | v2.11.0 | 8 findings; no tagged release with fixes at the time of review |

Work:

1. Upgrade `go-jose` and `x/net` to fixed versions (`go get`, `go mod tidy`).
2. For `excelize`: it is only used to **write** the Alibaba billing report in
   `internal/app/alibaba_billing.go` (`NewFile`, `CoordinatesToCellName`,
   `NewStyle`, `SetPanes`). Pick one, in this order of preference, and explain
   the choice in the PR:
   - a released `excelize` version that fixes all findings, if one now exists;
   - an `excelize` pseudo-version from its main branch at or after the newest
     fix Trivy lists (the scan on PR #1 lists fixes as
     `2.11.1-0.2026…` pseudo-versions, the newest from 2026-09-30); check its
     license and changelog, and note it as a temporary pin until a tagged
     release;
   - replace `excelize` with a small, tested XLSX writer that keeps the same
     output (header row style, frozen first row, same columns);
   - only as a last resort, a documented Trivy ignore per finding with the
     reason it is not reachable in write-only use, approved by Saheb in the PR.
3. Update `docs/dependency-license-audit.md` if a dependency is added or
   removed.

Acceptance:

- `make image-scan` passes locally and the `Images` workflow is green on the PR
  branch.
- The billing Excel export still downloads and opens; a test covers the
  generated file (sheet name, header row, at least one data row).
- No other dependency is upgraded beyond what `go mod tidy` requires.

### T2. Add `AGENTS.md`

- [x] Done — merged in [PR #3](https://github.com/Netriun-com/netriun-nexus/pull/3).

Why: this is the main control for how Codex works in this repository.

Work: add `AGENTS.md` at the repository root that states, briefly:

- The product direction: link to `docs/spec-v2.md`, `docs/architecture-v2.md`,
  `docs/roadmap.md`, and `task.md`. Work only on tasks in `task.md`.
- Workflow: one task per PR, no direct pushes to `main`, small PRs, PR
  description lists what changed, how it was tested, and anything left out.
- Required checks (the commands in "Rules for every task" above).
- Architecture rules from `docs/architecture-v2.md`: no provider calls on the
  HTTP read path for new code; new SQL goes into a store layer, not handlers;
  no new licensing code in this repository (licensing belongs to the Netriun
  Commercial Platform); no new code in `internal/licensing` or
  `internal/enterprise`.
- Code and docs change together; `docs/openapi.json` changes with routes.
- Security rules already followed by the code: strict CSP (no CDN scripts), no
  secrets in logs or commits, credentials stay encrypted at rest.
- All repository documents are written in English.

Acceptance: `AGENTS.md` exists, is under about 150 lines, and every rule above
is in it.

### T3. Make the README and docs honest

- [x] Done — merged in [PR #4](https://github.com/Netriun-com/netriun-nexus/pull/4).

Why: the README promises features that do not exist yet.

Work:

- README first paragraph: describe Nexus as a multi-cloud account
  orchestration platform (spec v2 wording). Remove "approvals" until phase 7
  ships it.
- Remove "Scheduled collection" claims. Today collection runs only when a page
  is opened or a refresh is requested; the scheduler arrives in phase 3.
- Replace the paragraph that points to the old product definition and roadmap
  with links to `docs/spec-v2.md`, `docs/architecture-v2.md`, and
  `docs/roadmap.md`.
- In `docs/product-definition.md` and `docs/self-hosted-spec.md`, add one line
  at the top saying they are superseded by `docs/spec-v2.md` where they differ.
- In `docs/index.md`, list `spec-v2.md`, `architecture-v2.md`, `roadmap.md`, and
  `archive/roadmap-v1.md`.

Acceptance: no document in the repository claims scheduled collection or
approvals as existing features; all links resolve.

### T4. Hotfix: stop page navigation from re-syncing and re-rendering

- [x] Done — merged in [PR #5](https://github.com/Netriun-com/netriun-nexus/pull/5).

Why: this is the loading problem Saheb sees. Today, opening or switching a
page in `internal/app/web/app.js` sets `inventoryRefreshRequested`,
`edsRefreshRequested.*`, `ossRefreshRequested`, or
`securityGroupRefreshRequested`, which starts a live provider refresh
(`startInventoryRefresh`, `startEDSRefresh`, `startOSSRefresh`,
`startSecurityGroupRefresh`). When the job finishes, the code calls
`await render()`, which re-renders the whole page and causes the flicker. The
real fix is phases 3 to 5; this task is a small, safe hotfix in the current UI.

Work:

1. Navigation and scope changes (page, provider, account, region) start a live
   refresh **only if** the snapshot for that scope is older than a staleness
   threshold (default 5 minutes) **and** no refresh for that scope is already
   queued or running. Use the snapshot timestamp the list endpoints already
   return; if one is missing, add it to the endpoint and to
   `docs/openapi.json`.
2. The refresh button and the refresh after a user action (for example after
   deleting a security group rule) keep refreshing immediately.
3. When a refresh finishes, update only the table rows and the status line for
   that scope instead of calling `render()` for the whole page. Keep the user's
   scroll position, open dialogs, filters, and selection.
4. If the user has left the page or changed scope, a finished refresh must not
   touch the screen (the existing run counters already help; keep that
   behavior).

Acceptance:

- Opening a page whose snapshot is fresh makes no `POST` to any `/refresh`
  endpoint (check in the browser network panel and describe it in the PR).
- Switching quickly between Instances, EDS, OSS, and Security groups does not
  create more than one refresh job per scope.
- A finished refresh does not reset scroll position or close an open dialog.
- The refresh button still works on every page.
- Backend tests still pass. Describe the manual test steps in the PR.

### T5. Enforce pull-request-only changes on `main` (Saheb)

- [x] Done — `main` is protected (pull request, one approval, required checks), confirmed by Saheb on 2026-11-17.

This task is for Saheb, not Codex: in the GitHub repository settings, protect
`main` so that changes need a pull request with one approving review and a
green `CI` workflow. The existing rule against force-push and deletion stays.

Acceptance: a direct push to `main` is rejected.

---

## Phase 2 — Extract licensing

Background: licensing becomes part of the shared **Netriun Commercial
Platform**, outside this repository. Its API is not defined yet. Nexus keeps
only `internal/entitlements`. Until the platform exists, every installation runs
with Community entitlements. Do not design the platform's API in this
repository.

### T6. Remove licensing and the Enterprise client from the runtime

- [x] Done — merged in [PR #9](https://github.com/Netriun-com/netriun-nexus/pull/9).

Work:

1. In `internal/app/app.go`, remove the `Enterprise *enterprise.Client` and
   `licenseClock` fields, the `enterprise.New(...)` call, the keyring, time
   floor, verifier, and `licensing.LoadDocument` code. Build entitlements with
   `entitlements.New(entitlements.CommunityProvider{Mode: cfg.deploymentMode})`
   (or the T7 provider once it exists).
2. In `internal/app/installation.go`, remove `advanceLicenseTimeFloor`,
   `persistLicenseTimeFloor`, and `startLicenseClockPersistence`. Keep
   `ensureInstallationID`.
3. In `installationStatus`, keep `installation_id`, `deployment_mode`,
   `edition`, and `license_status`; remove `license` and
   `enterprise_service_configured`. Update `docs/openapi.json` and any UI code
   that reads the removed fields.
4. In `internal/app/config.go`, remove the `ENTERPRISE_*` and
   `SPIFFE_ENDPOINT_SOCKET` settings. Remove them from `compose.yaml`,
   `.env.example`, and the Helm chart values and templates.
5. Remove the licensing and Enterprise parts of
   `internal/app/integration_test.go`; keep the tests that prove RBAC and
   entitlements are independent.
6. Do not change migrations `009` and `010`. Leave their columns and tables in
   place; they are no longer read or written.

Acceptance:

- `go list -deps ./cmd/nexus` contains neither `internal/licensing` nor
  `internal/enterprise`.
- A fresh install and an upgrade from a database at `0929f6a` both start
  cleanly and report `edition: community`.
- Community limits (identities, cloud accounts, retention) are still enforced
  and their tests pass.

### T7. Add the Netriun Commercial Platform provider stub

Work: add `internal/entitlements/commercialplatform` with a type that
implements `entitlements.Provider`. For now it always returns the Community
snapshot and has a clear `TODO` pointing to the future Netriun Commercial
Platform API. Wire it in `app.go` in place of `CommunityProvider`. No network
calls, no configuration variables yet.

Acceptance: unit test shows the provider returns the same snapshot as
`CommunityProvider`; the rule "if the licensing service is unavailable, Nexus
behaves as Community" is documented in the package comment.

### T8. Delete the extracted licensing code and documents

Do this only after Saheb confirms the code has been copied into the shared
licensing project (ask in the PR description).

Work: delete `internal/licensing`, `internal/enterprise`, `api/enterprise/v1`,
`deploy/docker/compose.enterprise-mtls.example.yaml`,
`deploy/helm/netriun-nexus/templates/enterprise-networkpolicy.yaml`, and the
licensing documents in `docs/` (`enterprise-licensing.md`,
`enterprise-service-authentication.md`, `license-issuer-architecture.md`,
`licensing-architecture.md`, `production-key-ceremony.md`,
`installation-binding-dr.md`, `commercial-license-requirements.md`,
`redis-licensing.md`, `enterprise-architecture.md`). Review each document
before deleting it: keep any part that describes Nexus itself (for example the
open-core and AGPL boundary) by moving it into a remaining document. Fix every
link that pointed to a deleted file. Keep `api/entitlements/v1`.

Acceptance: `go build ./...` and all tests pass; no broken links to deleted
files; `LICENSE.md` and `NOTICE` still describe the AGPL boundary correctly.

---

## Phase 3 — Durable jobs and sync (done)

All phase 3 tasks are merged: T9 (PR #19), T10 (PR #13), T11 (PR #14),
T12 (PR #15), T13 (PR #16), T14 (PR #18), T15 (PR #21), T16 (PR #20), plus the
queue test-isolation fix (PR #22). The full task text is in the git history of
this file. Phase 3 left these facts that phase 4 builds on:

- All collection runs as `scheduled.sync` jobs from `sync_state` rows, with
  lease-fenced writes, `account:*` and `provider:*` limits, and durable refresh
  receipts (`inventory_refreshes`).
- Pages never start a sync; the UI reads `GET /api/v1/sync-state`.
- OSS stays account-wide (`region = '*'`) because `ListBuckets` has no region
  filter; EDS users are scoped to directory endpoints.
- `Migrate()` rejects duplicate migration numbers. The next free number is
  `016`; check the latest `main` before choosing one.

---

## Phase 4 — One resource model (done)

All phase 4 tasks are merged: T17 (PR #25), T18 (PR #26), T21 (PR #27),
T19 (PR #29), T20 (PR #32), T22 (PR #31), T23 (PR #33), plus the lease-lost
test fix (PR #30). Released as v0.1.37. The full task text is in the git
history of this file. Phase 4 left these facts that phase 5 builds on:

- Every list reads `resources`; `instances` and `service_snapshots` are no
  longer written or read (deprecated by migration 018, dropped later).
- `resources.Reconcile` bumps `version` only when `name`, `state`, `tags`,
  `summary`, or `details` change, inside the caller's lease-fenced transaction.
  Unseen rows of the scope are deleted in the same transaction.
- `resource_relations.owner_service` records which service's collection owns an
  edge; the network sync owns instance → subnet, security group, and load
  balancer edges.
- Instance ids in the API and in page URLs are `resources.id`.
- Start/stop no longer writes a transitional state; the post-action refresh
  shows the observed state.
- The next free migration number is `019`; check the latest `main` before
  choosing one.

---

## Phase 5 — Live updates

Goal (from [docs/roadmap.md](docs/roadmap.md)): a resource change appears in an
open browser without a page refresh or flicker, and the UI no longer polls job
status. Design: [docs/architecture-v2.md](docs/architecture-v2.md) ("Live
updates to the UI").

Order and owners (Saheb may reassign; one task has one owner):

| Task | Depends on | Suggested owner |
| --- | --- | --- |
| T24 Durable change events written with every catalog change | — | Codex |
| T27 Phase 4 follow-ups | — | Collaborator |
| T25 `GET /api/v1/events` SSE stream | T24 | Collaborator |
| T26 UI patches rows from events and stops polling | T25 | Codex |

T24 and T27 can run in parallel.

Rules for this phase, in addition to "Rules for every task":

- All phase 3 and phase 4 rules still apply (numbered migrations, SQL in store
  packages, compatible response shapes, workspace and account filtering on
  every query, lease-fenced writes, PostgreSQL tests for every behavior change).
- An event is written in the same transaction as the change it describes, so a
  rolled-back or lease-lost collection never emits an event.
- `NOTIFY` payloads carry only ids (PostgreSQL limits a payload to 8000 bytes).
  Clients read event content from the database, filtered by access.
- A browser never receives an event for an account it cannot view, including
  after its access is removed while the stream is open.
- The `serve` and `all` roles serve the stream; the `worker` role never does.

### T24. Write durable change events with every catalog change

- [ ] Not started

Work:

1. Migration: `resource_events(id bigserial, workspace_id, account_id, type,
   service, region, resource_id, version, created_at)` with an index for
   replay by `(workspace_id, id)` and `ON DELETE CASCADE` to `cloud_accounts`.
   `resource_id` is not a foreign key, because deleted resources keep their
   delete event.
2. `resources.Reconcile` (and `ReconcileAllRegions`) insert one event per
   created, changed (`version` increased), or deleted row: types
   `resource.created`, `resource.updated`, `resource.deleted`. Unchanged rows
   whose only change is `seen_at` emit nothing. Relation changes emit
   `resource.updated` for the source row.
3. The sync handler writes `sync.started` and `sync.finished` (with
   `service`, `region`, success or error code) for each `scheduled.sync` job,
   in the transactions it already uses.
4. After inserting events, the same transaction runs one
   `pg_notify('nexus_events', workspace_id || ':' || max_event_id)`, so a
   listener learns that new events exist without the payload size limit.
5. A daily cleanup deletes events older than a configurable retention
   (default 24 hours), run by the existing daily `audit.retention` job or a
   sibling daily job; document the
   setting in the README and Helm values.

Acceptance: PostgreSQL tests that a sync with one new, one changed, one
unchanged, and one removed resource writes exactly three resource events; a
stale lease and a failed collection write none; a `LISTEN` connection in the
test receives the notification only after commit; cleanup removes only old
events; ten thousand unchanged resources emit no events.

### T25. Add the `GET /api/v1/events` stream

- [ ] Not started

Work:

1. Each API replica opens one dedicated PostgreSQL connection that `LISTEN`s
   on `nexus_events` (not one per browser) and reconnects with backoff. A hub
   wakes the connected streams of that workspace.
2. Each stream reads new rows from `resource_events` after its last delivered
   id, filtered by `workspace_id` and by `Policy.AccessibleAccountIDs` for
   `account.view`. Re-check access at least every 30 seconds and on every
   reconnect, so removed access stops events.
3. Server-Sent Events format with `id:` equal to the event id. A reconnect with
   `Last-Event-ID` replays the missed events; if that id is older than the
   retention window, or more than 1000 events are missing, send one
   `reset` event and continue from the newest id.
4. A heartbeat comment every 25 seconds. The route has no write deadline (use
   `http.ResponseController` to clear it for this route only; every other
   route keeps the 60 second `WriteTimeout`). Limit open streams per user
   (default 5) and return `429` above it.
5. Session cookie authentication like the other API routes; the route is in
   `docs/openapi.json`.
6. Document the infrastructure notes: Cloudflare Tunnel and Access keep the
   stream open because of the heartbeat; any ingress needs proxy buffering
   off and a read timeout above 60 seconds. Add the matching Helm notes.

Acceptance: tests for workspace and account isolation, access removed while a
stream is open, replay with `Last-Event-ID`, `reset` for a too-old id,
heartbeat, the per-user limit, a listener reconnect after the PostgreSQL
connection is killed, and that `worker` does not serve the route. A test with
two `serve` processes shows an event committed once reaching streams on both.

### T26. UI patches rows from events and stops polling

- [ ] Not started

Work:

1. The UI opens one `EventSource` per session after login and closes it on
   logout. It keeps a local store keyed by resource id.
2. On `resource.*` for a row on the current page, it fetches that one resource
   (`GET /api/v1/resources/{id}`) and patches the row in place; on
   `resource.deleted` it removes the row. Events for other pages are ignored.
   List responses may add `resource_id` and `version` fields (additive only)
   so service rows can be matched.
3. `sync.started` and `sync.finished` drive the sync status indicators. Remove
   the job and sync-state polling loops; keep one fallback reload of the
   current list after a `reset` event or after the stream has been down for
   more than 60 seconds.
4. Fast page switches must not cancel, duplicate, or flicker updates.

Acceptance: navigation tests pass; a browser test changes a resource in the
database through a sync and sees the row update without a page reload or a
full re-render; no `setTimeout` or `setInterval` remains that polls job or sync
status; recorded timings show no extra list requests while idle.

### T27. Phase 4 follow-ups

- [ ] Not started

Work:

1. Changing an account's provider deletes that account's `resources` rows of
   the old provider in the same transaction (today they are hidden by the
   provider join but never removed).
2. Alibaba network sync: a region where the SLB or ALB endpoint does not exist
   at all (DNS or connection failure before the first page) must not fail the
   whole network region. Use the official endpoint list to decide which
   products a region offers instead of guessing from the error; link it in the
   PR. Every other error still fails the region.
3. Fix the flaky `cmd/nexus` test "lease was not renewed for long handler"
   (1 second lease, 2.2 second handler) that can fail under a full `-race` run;
   do not lengthen the test without keeping what it proves.

Acceptance: PostgreSQL test for the provider change; recorded-response tests
for a region without ALB and a region without SLB; 20 consecutive full
`go test -race ./...` runs pass with the flaky test.

---

## Next

When phase 5 is done, Claude writes the phase 6 tasks (frontend rebuild) here
for review before work starts.
