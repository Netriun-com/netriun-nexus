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

The signed claims use this version-1 shape (the complete normative schema is
`api/enterprise/v1/license.schema.json`):

```json
{
  "license_id": "lic_example",
  "customer": {
    "organization_id": "org_example",
    "organization_name": "Example Company"
  },
  "product": "netriun-nexus",
  "edition": "enterprise",
  "deployment_mode": "self_hosted",
  "entitlements": {
    "features": ["advanced_sso", "cost_management"],
    "limits": {
      "workspaces": 10,
      "human_identities": 250,
      "cloud_accounts": 100,
      "audit_retention_days": 365
    }
  },
  "issued_at": "2026-10-06T00:00:00Z",
  "not_before": "2026-10-06T00:00:00Z",
  "expires_at": "2027-10-06T00:00:00Z",
  "installation_binding": {
    "type": "installation_id",
    "value": "ins_example"
  },
  "license_version": 1,
  "key_id": "nexus-2026-01"
}
```

The envelope uses `license` plus an Ed25519 `signature`; `key_id` is inside the
signed claims. The verifier signs RFC 8785/JCS bytes, rejects duplicate or
unknown fields and unknown versions, and applies deterministic skew/grace
tests. See [Enterprise licensing](enterprise-licensing.md).

## Storage and reload

The Helm chart accepts the license through a Kubernetes Secret mounted read-only
as a file. Host/Compose deployments accept the same file contract. The signed
document remains the source of truth and is evaluated at process startup;
replacement requires a controlled application restart in M4.

The installation receives a generated stable PostgreSQL installation ID.
Enterprise licenses are bound to it. Backup and restore procedures preserve it.

## Failure behavior

- No license: Community entitlements.
- Valid license: licensed entitlements.
- Expired within 14 days: grace status, licensed features available, visible warning.
- Expired after grace: Community gates apply; existing data remains readable.
- Invalid signature, product, binding, or schema: Community gates apply and the
  cause is exposed to administrators without logging the license document.
- License parser or storage failure: application remains available in Community mode.

Denied limit mutations return stable machine-readable errors without logging
license signatures, cloud secrets, or sensitive payloads. Persistent audit of
license status transitions is a future administration enhancement.

## Distribution and supply chain

The canonical Community image is `ghcr.io/netriun-com/netriun-nexus`. Release
images are built once, scanned, published, and keylessly signed. Kubernetes
production deployments should pin an immutable version or digest and verify
provenance in their delivery policy. Enterprise uses this public core plus a
separately built, licensed proprietary component; Netriun Cloud uses the public
core plus private service infrastructure. Their integration contracts and
version compatibility must be explicit before the first Enterprise build.

The Open Source Work license and proprietary boundaries are defined in the
root `LICENSE.md`. Valkey resolved the former Redis licensing blocker. Remaining
beta gates are tracked in [the roadmap](roadmap.md).
