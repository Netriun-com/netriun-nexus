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
- [ ] Select and publish the repository/redistribution license.
- [ ] Approve final Community limits and commercial packaging.

Exit: product and legal decisions permit public self-hosted distribution.

## M2 — Entitlement foundation

- [x] Add typed feature, limit, edition, and status models.
- [x] Implement the Community entitlement provider.
- [x] Add backend decision helpers and stable API error responses.
- [x] Add unit and integration tests that prove RBAC and entitlement are independent.
- [x] Expose administrator-safe entitlement status through the API.

Exit: Community behavior is centrally enforced without license parsing.

## M3 — Offline Enterprise license

- [ ] Implement canonical payload and Ed25519 signature verification.
- [ ] Add key rotation by `key_id` and golden interoperability vectors.
- [ ] Add installation identity, file loading, atomic replacement, and status audit.
- [ ] Implement grace/expired/invalid fallback behavior.
- [ ] Add an offline internal license issuer outside this repository.

Exit: a test license deterministically changes entitlements with no network call.

## M4 — Product gates and limits

- [ ] Enforce identity, workspace, cloud-account, and retention limits.
- [ ] Gate custom access roles, OIDC/SAML, billing reports, exports, and schedules.
- [ ] Cover every gated mutation with bypass-resistant integration tests.
- [ ] Preserve read access and safe degradation after license expiry.

Exit: no premium API can be activated by hiding or modifying the web UI.

## M5 — Administration experience

- [ ] Add edition/status/limits/license metadata to Workspace Settings.
- [ ] Add license installation/replacement workflow for self-hosted administrators.
- [ ] Explain locked capabilities and current usage without exposing secrets.
- [ ] Add grace and expiry warnings with remediation links.

Exit: an operator can diagnose and replace a license without database access.

## M6 — Self-hosted distribution

- [ ] Finalize public image access, versioning, SBOM/provenance, and signature docs.
- [ ] Add Community and licensed examples to Compose and Helm.
- [ ] Document install, upgrade, backup/restore, rollback, SMTP, and Cloudflare paths.
- [ ] Add migration and compatibility policy.

Exit: a new operator can install and safely upgrade using published artifacts only.

## M7 — Isolated staging

- [ ] Create `nexus-selfhosted-dev` with isolated state and secrets.
- [ ] Deploy an immutable signed image.
- [ ] Pass fresh-install, upgrade, rollback, license-state, and cloud smoke tests.
- [ ] Record release evidence and operational recovery time.

Exit: all checks in [the staging specification](staging.md) pass.

## M8 — Community beta

- [ ] Resolve all release-blocking security and data-loss findings.
- [ ] Publish the selected license, support boundary, and known limitations.
- [ ] Tag the release and publish checksums, signatures, chart, and release notes.
- [ ] Open a documented feedback and vulnerability-reporting path.

Exit: the Community self-hosted beta is publicly installable and supportable.
