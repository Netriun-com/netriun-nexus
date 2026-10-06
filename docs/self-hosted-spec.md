# Self-hosted and entitlement specification

This specification defines the technical boundary between Community and licensed
self-hosted editions. It is deliberately independent of billing providers and
cloud-service implementations.

## Runtime mode

The application has an explicit deployment mode:

- `cloud`: operated by Netriun; entitlements may come from the managed control plane.
- `self_hosted`: operated by the customer; starts with Community entitlements and
  optionally loads a signed offline license.

Deployment mode is configuration, not a UI-only flag. A self-hosted package must
remain fully operable without outbound access to a Netriun licensing service.

## Entitlement boundary

All edition logic belongs behind an internal entitlement service (initially
`internal/entitlements`). Application handlers call it after authentication and
workspace RBAC have succeeded.

The service exposes three concepts:

- feature decision: whether a stable feature key is enabled;
- limit decision: the allowed value and current usage for a stable limit key;
- installation status: Community, licensed, grace, expired, or invalid.

Handlers must not compare plan names or parse license files directly. UI checks
are advisory only; every premium API operation and limit-changing mutation is
enforced on the server. Denials use a stable machine-readable error code and may
include feature, limit, current usage, and allowed value.

Authorization order is:

```text
authenticated identity -> workspace boundary -> RBAC/access policy -> entitlement -> action
```

## Offline license format

Enterprise self-hosted licenses use a signed canonical JSON envelope. Ed25519 is
the initial signature algorithm. The image contains only trusted public keys;
Netriun keeps private signing keys outside the source repository and CI system.

The signed payload contains at least:

```json
{
  "schema_version": 1,
  "product": "nexus",
  "license_id": "lic_example",
  "customer": "Example Company",
  "issued_at": "2026-10-06T00:00:00Z",
  "not_before": "2026-10-06T00:00:00Z",
  "expires_at": "2027-10-06T00:00:00Z",
  "maintenance_until": "2027-10-06T00:00:00Z",
  "features": ["sso", "custom_access_roles", "billing_reports"],
  "limits": {
    "workspaces": 10,
    "human_identities": 250,
    "cloud_accounts": 100,
    "audit_retention_days": 365
  },
  "installation_id": null,
  "key_id": "nexus-2026-01"
}
```

The envelope includes the payload, algorithm, key ID, and signature. Canonical
serialization is part of the format and must be covered by golden test vectors.
Unknown keys are ignored for forward compatibility; unknown schema versions are
rejected. Clock handling, grace dates, and status transitions are deterministic
and tested.

## Storage and reload

The Helm chart accepts the license through a Kubernetes Secret mounted read-only
as a file. Docker Compose accepts the same file contract. The database stores
only normalized status needed for audit/display; the signed document remains the
source of truth. License replacement is atomic and can be picked up by an
explicit reload or process restart in the first implementation.

The installation receives a generated stable installation ID. Binding a license
to that ID is optional. Backup and restore procedures must preserve it.

## Failure behavior

- No license: Community entitlements.
- Valid license: licensed entitlements.
- Expired within 14 days: grace status, licensed features available, visible warning.
- Expired after grace: Community gates apply; existing data remains readable.
- Invalid signature, product, binding, or schema: Community gates apply and the
  cause is exposed to administrators without logging the license document.
- License parser or storage failure: application remains available in Community mode.

Every status change and denied premium mutation is audited without storing
license signatures, cloud secrets, or sensitive payloads.

## Distribution and supply chain

The canonical Community image is `ghcr.io/netriun-com/netriun-nexus`. Release
images are built once, scanned, published, and keylessly signed. Kubernetes
production deployments should pin an immutable version or digest and verify
provenance in their delivery policy. Enterprise uses this public core plus a
separately built, licensed proprietary component; Netriun Cloud uses the public
core plus private service infrastructure. Their integration contracts and
version compatibility must be explicit before the first Enterprise build.

The Open Source Work license and proprietary boundaries are defined in the
root `LICENSE.md`. The public self-hosted beta remains blocked until the Redis
runtime licensing decision, chart, image visibility, upgrade path,
backup/restore, third-party notices, and entitlement tests meet the acceptance
criteria in [the roadmap](roadmap.md).
