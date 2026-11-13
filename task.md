# Current tasks

Phase: **2 — Extract licensing** (T8 left) and **3 — Durable jobs and sync** (see [docs/roadmap.md](docs/roadmap.md)).
Written: 2026-10-30 (phases 1–2), 2026-11-09 (phase 3).

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

Start with **T1**, then **T2**. T3 to T5 can follow in any order. Phase 2
(T6 to T8) can start after T2 is merged.

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

## Phase 3 — Durable jobs and sync

Goal (from [docs/roadmap.md](docs/roadmap.md)): pages never trigger sync, a
background scheduler keeps data fresh, jobs survive restarts, and one large
account cannot delay others. Design: [docs/architecture-v2.md](docs/architecture-v2.md)
("Data model", "Sync engine", "Code layout").

Order: T9 can run any time. T10 → T11 → T12 → T13 → T14 → T15 in order; each
builds on the previous one. T16 can run after T12.

Rules for this phase, in addition to "Rules for every task":

- Every new table is added by a new numbered migration in
  `internal/app/migrations/`. Never edit an existing migration.
- New SQL goes into a store layer (`internal/jobs`, `internal/sync`), not into
  HTTP handlers.
- Keep existing HTTP routes and response shapes working until the UI no
  longer uses them. If a response changes, update `docs/openapi.json`.
- Every behavior change has a test against PostgreSQL (the CI integration
  setup), not only unit tests with fakes.

### T9. CI hygiene: run the frontend tests and fix the legacy migration test

Why: `scripts/test-navigation.cjs` (34 tests from PR #5 and PR #8) does not run
in CI, and `cmd/migrate-legacy`'s opt-in test reads a file that no longer
exists (`internal/app/schema.sql`), as found in PR #2.

Work:

1. Add a CI step that runs `node --test scripts/test-navigation.cjs` with a
   pinned Node version and a SHA-pinned setup action, like the other actions
   in `.github/workflows/ci.yml`.
2. Fix `cmd/migrate-legacy/main_test.go` so it builds its schema from the
   current migrations instead of `internal/app/schema.sql`. Keep the test
   opt-in (`TEST_MIGRATION_DATABASE_URL`).

Acceptance: the CI log shows the 34 navigation tests running; the legacy test
passes locally against an isolated database.

### T10. Add the durable job queue (`jobs` table and `internal/jobs`)

- [x] Done — merged in [PR #13](https://github.com/Netriun-com/netriun-nexus/pull/13).

Work:

1. Migration: `jobs` table with at least `id`, `kind`, `payload` (JSONB),
   `status` (`queued`, `running`, `succeeded`, `failed`), `priority`,
   `attempts`, `max_attempts`, `run_after`, `locked_by`, `locked_until`,
   `concurrency_key`, `dedupe_key` (unique while queued or running),
   `last_error`, `created_at`, `updated_at`. Index for claiming due jobs.
2. Package `internal/jobs` with: `Enqueue` (returns the existing job when the
   same `dedupe_key` is already queued or running), `Claim` using
   `FOR UPDATE SKIP LOCKED` that respects per-`concurrency_key` limits,
   lease renewal, `Complete`, `Fail` (exponential backoff until
   `max_attempts`), and reclaiming jobs whose lease expired.
3. No caller uses it yet. No HTTP changes.

Acceptance (tests against PostgreSQL):

- Two concurrent claimers never get the same job.
- A job whose worker stops renewing its lease is picked up by another claimer.
- A concurrency key with limit N never has more than N running jobs.
- Duplicate `Enqueue` with the same `dedupe_key` returns the same job.
- Failed jobs back off and stop after `max_attempts`.

### T11. Add the `nexus worker` role

- [x] Done — merged in [PR #14](https://github.com/Netriun-com/netriun-nexus/pull/14).

Work:

1. `cmd/nexus` accepts a role: `serve` (HTTP only), `worker` (job loop only),
   or `all` (both). Default is `all`, so current installs keep working
   unchanged. Choose flag or environment variable and document it.
2. The worker runs a pool of goroutines that claim and run jobs from T10 by
   `kind`, with a registry of handlers. Graceful shutdown on SIGTERM stops
   claiming, lets running jobs finish within a timeout, then releases leases.
3. Compose keeps one combined container. The Helm chart gets an option to run
   a separate worker Deployment (off by default).
4. Health: `/readyz` for `serve`; a simple liveness signal for `worker`.

Acceptance: a test job kind runs in `all` and in separate `serve` + `worker`
processes; killing the worker mid-job leads to the job being re-run after the
lease expires; Helm lint and template pass with the worker option on and off.

### T12. Add `sync_state` and the scheduler

- [x] Implemented in this PR, including the approved account-wide `*` scopes until T14.

Work:

1. Migration: `sync_state` with one row per account + service + region and
   `interval`, `last_started_at`, `last_success_at`, `last_error`,
   `last_error_code`, `next_due_at`, `empty` (no resources found last time).
2. Rows are created and removed when accounts are added, deleted, or their
   region selection changes, and for every service the provider supports
   today (compute for all four providers; EDS desktops, EDS users, OSS, and
   security groups for Alibaba).
3. A scheduler loop in the worker enqueues one sync job per due row, with a
   `dedupe_key` per row, so a row never has two jobs. Default intervals from
   the architecture: instances and desktops 5 minutes; security groups 15
   minutes; buckets 60 minutes; regions marked empty 6 hours.
4. In this task the sync job handlers only call today's existing collection
   code for that account, service, and region. Moving the code is T13 and T14.

Acceptance: with the worker running and no browser open, every enabled row is
synced on schedule; empty regions move to the 6-hour interval; stopping and
starting the worker does not lose or duplicate scheduled work.

### T13. Move compute inventory refresh onto the queue

Work:

1. Compute sync becomes one job per account + region (job kind for example
   `sync.compute`), using the existing provider collectors and reconciliation
   (failed regions keep their previous rows).
2. `POST /api/v1/inventory/refresh` enqueues high-priority jobs for the
   selected accounts and returns a job id the existing status endpoint can
   report on; the response shape stays compatible with the current UI.
3. Errors are written to `sync_state` and `cloud_accounts.sync_error` with a
   fresh context, not the job's possibly cancelled context.
4. Remove the Valkey-based inventory job runner in `refresh_jobs.go` and the
   global `accountSlots`/`regionSlots` once nothing uses them.

Acceptance: a refresh survives a server restart; one slow account does not
delay another account's refresh (test with a fake slow provider); existing
inventory integration tests still pass.

### T14. Move EDS, OSS, and security group refresh onto the queue

Same pattern as T13 for `eds_refresh.go`, OSS, and security groups: one job per
account + service + region, existing refresh endpoints enqueue high-priority
jobs and keep their response shapes, errors go to `sync_state`. Remove the old
in-process runners when unused.

Acceptance: as in T13, for each of the three services.

### T15. Pages stop triggering sync

Work:

1. In `internal/app/web/app.js`, page navigation and scope changes never start
   a refresh. Only the refresh button and the post-action follow-ups from
   PR #8 do.
2. Each list shows "last synced" and any per-region error from `sync_state`
   (add a small read endpoint if needed, documented in `docs/openapi.json`).
3. Update `scripts/test-navigation.cjs` for the new rule.

Acceptance: opening any page sends no refresh request; data still becomes fresh
on its own within its interval; the README and in-app docs describe the
scheduler accurately.

### T16. Run audit retention as a scheduled job

Why: audit deletion only runs at the end of `collectAccounts`
(`internal/app/collector.go`), so with no refreshes, retention never runs.

Work: a daily job per workspace that deletes audit records older than
`audit_retention_days`, scheduled by the T12 scheduler; remove the deletion
from `collectAccounts`.

Acceptance: integration test shows old audit rows are removed by the job and
not by a refresh.

---

## Next

When phase 3 is done, Claude writes the phase 4 tasks (one `resources` model
and `/api/v1/resources`) here for review before work starts.
