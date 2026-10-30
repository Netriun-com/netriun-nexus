# Current tasks

Phase: **1 — Stabilize** and **2 — Extract licensing** (see [docs/roadmap.md](docs/roadmap.md)).
Written: 2026-10-30. Baseline: `main` at `0929f6a`.

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

## Next

When phases 1 and 2 are done, Claude writes the phase 3 tasks (durable jobs,
`sync_state`, scheduler, `nexus worker`) here for review before work starts.
