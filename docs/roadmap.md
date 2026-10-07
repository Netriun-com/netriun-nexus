# Netriun Nexus delivery roadmap

This is the execution order for the self-hosted product. Each milestone has an
observable exit condition; later milestones should not bypass an incomplete
security or data-safety prerequisite.

## M0 — Repository and artifact foundation

- [x] Transfer the repository to `Netriun-com/netriun-nexus`.
- [x] Move the canonical image name to `ghcr.io/netriun-com/netriun-nexus`.
- [x] Add race/integration CI, exact-artifact vulnerability scanning, and signing.
- [x] Run the image as an unprivileged user on a minimal runtime base.
- [x] Make the GHCR package anonymously pullable.
- [x] Protect default branches against force-push and deletion.
- [x] Publish and deploy the first signed organization release (`v0.1.27`).

## M1 — Product contract

- [x] Define Cloud, Community, and Enterprise delivery forms.
- [x] Define stable features, limits, expiry behavior, and data-access rules.
- [x] Define staging topology and release promotion.
- [x] Select and publish the AGPL-3.0-only Open Source Work license and explicit proprietary boundaries.
- [ ] Approve final Community limits and commercial packaging.

Exit: product and legal decisions permit public self-hosted distribution.

## M2 — Entitlement foundation

- [x] Add typed feature, limit, edition, and status models.
- [x] Implement the Community entitlement provider.
- [x] Add backend decision helpers and stable API error responses.
- [x] Add unit and integration tests that prove RBAC and entitlement are independent.
- [x] Expose administrator-safe entitlement status through the API.

Exit: Community behavior is centrally enforced without license parsing.

## M3 — Open-core licensing boundary

- [x] Publish the unmodified AGPL-3.0-only license text.
- [x] Define the exact Community/Core, Enterprise, and Netriun Cloud path boundary.
- [x] Map current packages into the target `core/community/providers/api/cli` architecture.
- [x] Add source SPDX identifiers and preserve required dependency notices.
- [x] Audit linked Go dependencies and declared container/runtime licenses.
- [x] Record unresolved legal/product decisions without silently selecting terms.

Exit: every current path has a documented licensing destination, the Open
Source Work is redistributable under AGPL-3.0-only, and proprietary code has a
non-AGPL boundary.

## M4 — Offline Enterprise license

- [x] Replace the Community Redis runtime with pinned Valkey and preserve rollback storage.
- [x] Implement RFC 8785 canonical payload and Ed25519 signature verification.
- [x] Add versioned `key_id` rotation states and interoperability/security vectors.
- [x] Add stable PostgreSQL installation identity and read-only file loading.
- [x] Implement five-minute skew, 14-day grace, and non-destructive Community fallback.
- [x] Define the separate `/enterprise/v1` HTTP/JSON process contract.
- [x] Enforce Community human-identity, cloud-account, and retention limits.
- [ ] Add an offline internal license issuer outside this repository.
- [ ] Perform the production key ceremony and add only its public key to Core.
- [ ] Add atomic live reload and persistent license-status audit events.

Core implementation exit achieved: an ephemeral signed test license
deterministically changes entitlements with no network call. Production license
activation remains blocked on the private issuer and public-key ceremony.

## M5 — Production license trust and Enterprise security

- [x] Define the production Ed25519 key ceremony, public metadata, custody,
  recovery, rotation and compromise procedure.
- [ ] Run the ceremony in an approved offline hardware custody environment and
  commit only the real public key.
- [x] Define the independent proprietary issuer architecture and CLI contract.
- [x] Add runtime expiry re-evaluation, persistent time floor and clock-rollback
  protection.
- [x] Define the clone/restore/DR policy and commercial-license engineering
  requirements.
- [x] Compare service authentication choices and recommend mTLS workload
  identities plus NetworkPolicy defense in depth.
- [ ] Obtain owner approval for the authentication recommendation, then
  implement mTLS, replay controls and NetworkPolicy tests.
- [x] Keep legacy Redis storage through the rollback window.

Exit: ceremony artifacts and service authentication pass security review; only
the public production trust root enters Core; no customer license is issued.

## M6 — Product gates and limits

- [ ] Decide and enforce the installation-wide workspace limit without breaking public registration.
- [x] Enforce identity, cloud-account, and retention limits.
- [ ] Gate only the approved new Enterprise capabilities; existing AGPL SSO,
  roles, billing reports, and exports remain Core and must not be gated.
- [ ] Cover every gated mutation with bypass-resistant integration tests.
- [ ] Preserve read access and safe degradation after license expiry.

Exit: no premium API can be activated by hiding or modifying the web UI.

## M7 — Administration experience

- [ ] Add edition/status/limits/license metadata to Workspace Settings.
- [ ] Add license installation/replacement workflow for self-hosted administrators.
- [ ] Explain locked capabilities and current usage without exposing secrets.
- [ ] Add grace and expiry warnings with remediation links.

Exit: an operator can diagnose and replace a license without database access.

## M8 — Self-hosted distribution

- [ ] Finalize public image access, versioning, SBOM/provenance, and signature docs.
- [ ] Add Community and licensed examples to Compose and Helm.
- [ ] Document install, upgrade, backup/restore, rollback, SMTP, and Cloudflare paths.
- [ ] Add migration and compatibility policy.

Exit: a new operator can install and safely upgrade using published artifacts only.

## M9 — Isolated staging

- [ ] Create `nexus-selfhosted-dev` with isolated state and secrets.
- [ ] Deploy an immutable signed image.
- [ ] Pass fresh-install, upgrade, rollback, license-state, and cloud smoke tests.
- [ ] Record release evidence and operational recovery time.

Exit: all checks in [the staging specification](staging.md) pass.

## M10 — Community beta

- [ ] Resolve all release-blocking security and data-loss findings.
- [ ] Publish the selected license, support boundary, and known limitations.
- [ ] Tag the release and publish checksums, signatures, chart, and release notes.
- [ ] Open a documented feedback and vulnerability-reporting path.

Exit: the Community self-hosted beta is publicly installable and supportable.
