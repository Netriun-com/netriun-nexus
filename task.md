# Current tasks

Phase: **4 — One resource model** (see [docs/roadmap.md](docs/roadmap.md)). Phase 2 still has T8 open.
Written: 2026-10-30 (phases 1–2), 2026-11-09 (phase 3), 2026-11-16 (phase 4).

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

Phases 1 and 3 are done. Phase 4 starts with **T17** and **T18** in parallel
(see the order table in phase 4). T8 waits for Saheb's confirmation.

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

## Phase 4 — One resource model

Goal (from [docs/roadmap.md](docs/roadmap.md)): one `resources` catalog and one
`resource_relations` table replace the split between the `instances` table and
the `service_snapshots` JSON blobs; every list reads from `resources` in under
one second; Alibaba and AWS also sync networks, subnets, and load balancers with
their relations. Design: [docs/architecture-v2.md](docs/architecture-v2.md)
("Data model", "Provider adapters", "API, CLI, and authentication").

Order and owners (Saheb may reassign; one task has one owner):

| Task | Depends on | Suggested owner |
| --- | --- | --- |
| T17 `resources` tables and store | — | Codex |
| T18 Provider interface and compute adapters | — | Collaborator |
| T21 `GET /api/v1/resources` | T17 | Codex |
| T19 Compute sync writes `resources` | T17, T18 | Codex |
| T20 EDS, OSS, and security groups write `resources` | T19 | Codex |
| T22 Networks, subnets, and load balancers for Alibaba and AWS | T18, T19 | Collaborator |
| T23 Every list reads `resources`; old writes stop | T20, T21, T22 | Codex |

T17 and T18 can run in parallel. T21 can run in parallel with T19.

Rules for this phase, in addition to "Rules for every task":

- All phase 3 rules still apply (new numbered migrations only, SQL in store
  packages, compatible response shapes, PostgreSQL tests for every behavior
  change).
- Two tasks may be open at once. Before adding a migration, merge the latest
  `main` and take the next free number.
- During T19 to T22 the old tables keep being written (dual write) so the
  current UI keeps working. Only T23 stops the old writes. Old tables are not
  dropped in this phase.
- Every `resources` query filters by `workspace_id` and by the caller's
  accessible accounts (`Policy.AccessibleAccountIDs`). Never look up a
  resource by id alone.
- Every write to `resources` is fenced by the job lease, like
  `ReconcileInventory` and `SaveServiceSnapshot` today.
- Do not guess provider APIs: link the official documentation for every new
  provider call in the PR description, and cover it with recorded-response
  tests (no real cloud credentials in CI).

### T17. Add the `resources` and `resource_relations` tables and store

- [x] Done — schema, transactional reconciliation, scoped reads, and cursor pagination; runtime wiring remains in later tasks.

Work:

1. Migration: `resources` with the columns from the architecture
   (`workspace_id`, `account_id`, `provider`, `service`, `kind`, `region`,
   `provider_id`, `name`, `state`, `tags` JSONB, `summary` JSONB, `details`
   JSONB, `seen_at`, `version`, `created_at`, `updated_at`), unique on
   `(account_id, service, region, kind, provider_id)`, foreign keys with
   `ON DELETE CASCADE` to `cloud_accounts`.
2. Migration: `resource_relations(from_id, to_id, relation)` with a unique key,
   cascade deletes, and indexes on `from_id` and `to_id`.
3. Indexes: `(workspace_id, account_id, service, region)` for lists,
   `(workspace_id, kind, name, id)` for sorted pages, and `pg_trgm` GIN indexes
   on `name` and `provider_id` for search. Use `CREATE EXTENSION IF NOT EXISTS
   pg_trgm`; document in the README that the database role needs permission
   to create it (it is a trusted extension since PostgreSQL 13).
4. A store package `internal/resources` with:
   - `Reconcile(ctx, tx, scope, items, relations)`: upsert what the provider
     returned and delete only unseen rows of the same account, service, and
     region (and kind), in one transaction, only after a fully successful
     collection. It takes the caller's transaction so the sync handler can
     fence it with the job lease.
   - `version` increases only when `name`, `state`, `tags`, `summary`, or
     `details` actually change (phase 5 will emit events from it).
   - `List(ctx, workspace, accounts, filter, cursor, limit)` returning
     `summary` only, and `Get(ctx, workspace, accounts, id)` returning
     `details` and relations.
5. No writer or HTTP route uses the store yet.

Acceptance: PostgreSQL tests for reconcile (upsert, unseen delete per scope,
unchanged rows keep their `version`, a failed scope leaves rows untouched),
workspace and account isolation in `List` and `Get`, cursor pagination with no
duplicates or gaps while rows are inserted, and `EXPLAIN` showing the
`pg_trgm` index used for a `q` search.

### T18. Add the provider interface and compute adapters

Work:

1. `internal/providers` defines the interface from the architecture without
   `Apply` (that comes in phase 7):
   `Services() []ServiceSpec`, `List(ctx, account, service, region)
   ([]Resource, []Relation, error)`, `Get(ctx, account, kind, id)`.
   `Resource` and `Relation` match the T17 columns but do not import the store.
2. One package per provider (`internal/providers/aws`, `alibaba`, `azure`,
   `gcp`) wrapping today's `internal/cloud` clients. In this task each adapter
   implements the `compute` service only (kind `instance`), mapping provider
   objects to the shared fields and keeping the provider's own type name in
   `summary` (for example `ECS instance`, `EC2 instance`).
3. A registry that returns the adapter for a provider name, so callers never
   `switch` on provider names.
4. Recorded-response tests for each adapter (HTTP fixtures or SDK test
   doubles), including pagination and an error page. CI uses no real
   credentials.
5. No sync code changes yet.

Acceptance: each adapter's `List` returns the same instances as today's
`internal/cloud` inventory function for the same recorded responses; `go test`
passes without network access.

### T19. Compute sync writes `resources`

Work:

1. The `compute` sync handler collects through the T18 adapter and, in the same
   lease-fenced transaction as today's `instances` reconcile, calls the T17
   store to reconcile `resources` (kind `instance`).
2. Keep writing the `instances` table (dual write); no read path changes.
3. Failed regions keep their previous `resources` rows, exactly like
   `instances`.
4. Remove the `switch provider` blocks in the compute collector that the
   adapter registry replaces.

Acceptance: after a sync, `instances` and `resources` contain the same
instances for every account and region (PostgreSQL test with recorded
responses); a stale lease writes neither; existing compute integration tests
still pass.

### T20. EDS, OSS, and security groups write `resources`

Work: same pattern as T19 for EDS desktops (kind `desktop`), EDS users (kind
`directory_user`), OSS buckets (kind `bucket`, region from the bucket's own
location), and security groups (kind `security_group`, with rules in
`details`). Add these services to the Alibaba adapter. Keep writing
`service_snapshots` (dual write).

Acceptance: as in T19, for each service; the existing EDS, OSS, and security
group integration tests still pass.

### T21. Add `GET /api/v1/resources` and `GET /api/v1/resources/{id}`

Work:

1. `GET /api/v1/resources` with filters `kind`, `account_ids`, `region`,
   `state`, `service`, and `q` (search on name and provider id using the
   `pg_trgm` index), sorted by name, with an opaque cursor and `limit`
   (default 50, maximum 200). Returns `summary`, never `details`.
2. `GET /api/v1/resources/{id}` returns `details` and the resource's relations
   (both directions, with the related resource's `kind` and `name`).
3. Both routes filter by workspace and the caller's accessible accounts; an
   inaccessible or other-workspace id returns 404.
4. Document both routes in `docs/openapi.json`.

Acceptance: PostgreSQL tests for every filter, cursor paging, 404 isolation,
and that `details` never appears in the list; with 100,000 seeded resources a
filtered page responds in under one second locally (report the measurement in
the PR).

### T22. Sync networks, subnets, and load balancers for Alibaba and AWS

Work:

1. New `network` service in `sync_state` for Alibaba and AWS accounts, per
   region, default interval 15 minutes (architecture "Sync engine"),
   configurable like the other intervals.
2. Adapters list networks (AWS VPC, Alibaba VPC), subnets (AWS subnet,
   Alibaba VSwitch), and load balancers (AWS ELBv2; Alibaba CLB/SLB and ALB),
   with kinds `network`, `subnet`, `load_balancer`.
3. Relations: `subnet in_network network`, `instance in_subnet subnet`,
   `instance uses_rule security_group`, `load_balancer in_network network`,
   `instance behind_lb load_balancer`. Relations to resources from other
   services are written when both ends exist; a missing end is skipped, not an
   error.
4. Read-only: no create, change, or delete calls (phase 7).
5. Link the official API reference for every call in the PR description.

Acceptance: recorded-response tests per provider and kind; PostgreSQL test
that a full sync produces the expected relations; a failed region keeps its
previous network rows; the existing services are unaffected.

### T23. Every list reads `resources`; old writes stop

Work:

1. The existing endpoints (`/instances`, EDS, OSS, security groups, summary,
   reports) read from `resources` and keep their response shapes, so the
   current UI works unchanged. Instance actions and details look up the
   resource through `resources`.
2. Search uses the `pg_trgm` index; remove the `ILIKE` scans on `instances`.
3. Sync stops writing `instances` and `service_snapshots`. The tables stay
   (dropped in a later phase after a release); add a comment in a new
   migration marking them deprecated.
4. Update the README and in-app docs.

Acceptance: every existing integration and navigation test passes against
`resources`; with 100,000 seeded resources every list endpoint responds in
under one second locally (measurements in the PR); no code path writes
`instances` or `service_snapshots`.

---

## Next

When phase 4 is done, Claude writes the phase 5 tasks (SSE live updates) here
for review before work starts.
