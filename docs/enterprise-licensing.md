# Offline Enterprise licensing

Status: M5 trust hardening implemented in Core; the production ceremony is
specified but blocked on an approved offline hardware custody environment.

## Trust model

Core verifies a bounded JSON license locally with RFC 8785 JSON
Canonicalization Scheme (JCS) and Ed25519. It performs no network call. The
private signing key must never enter this repository, a Nexus image, a Core
binary, or Community CI. Core embeds only a versioned public keyring.

The embedded keyring is intentionally empty until Netriun completes the
approved [production key ceremony](production-key-ceremony.md). Tests generate
ephemeral Ed25519 keys in memory. Core has no production signing function.

## Envelope and signed claims

The signature covers the JCS canonical bytes of the `license` object only:

```json
{
  "license": {
    "license_id": "lic_example",
    "customer": {
      "organization_id": "org_example",
      "organization_name": "Example Organization"
    },
    "product": "netriun-nexus",
    "edition": "enterprise",
    "deployment_mode": "self_hosted",
    "entitlements": {
      "features": ["advanced_sso", "cost_management"],
      "limits": {"cloud_accounts": 100, "human_identities": 250}
    },
    "issued_at": "2026-10-06T12:00:00Z",
    "not_before": "2026-10-06T12:00:00Z",
    "expires_at": "2027-10-06T12:00:00Z",
    "installation_binding": {
      "type": "installation_id",
      "value": "ins_example"
    },
    "license_version": 1,
    "key_id": "production-2026-01"
  },
  "signature": {
    "algorithm": "Ed25519",
    "value": "base64url-without-padding"
  }
}
```

The example signature is a placeholder and is not valid. Input is limited to
64 KiB, must be one I-JSON-compatible value, may not contain duplicate or
unknown members, and uses canonical UTC RFC3339 timestamps with whole seconds.
Unknown features, limits, license versions, algorithms, and keys fail closed.

## Key rotation and revocation

Every license signs `key_id`. The embedded keyring has its own version and each
Ed25519 public key records an immutable ID, algorithm, creation timestamp,
`license_signing` usage and an `active`, `retired`, or `revoked` state:

- active keys verify current licenses;
- retired keys continue verifying already issued licenses;
- revoked keys fail immediately, even when a signature is otherwise valid.

Initial revocation is shipped through a Core application update. A signed
offline trust bundle is intentionally deferred. Production key generation,
custody, access control, backup, destruction, and issuance audit belong to an
offline proprietary issuer and operating procedure.

## Time and degradation behavior

The verifier allows five minutes of clock skew. After `expires_at`, the license
enters a 14-day grace period and its licensed features continue to operate.
After grace, or for an invalid license, Core falls back to Community behavior.
Core re-evaluates the loaded document during entitlement checks, so a running
process enters grace/expiry without a restart. A persistent installation time
floor plus an in-process high-water mark rejects material clock rollback.

| State | Enterprise capabilities | Core/data behavior |
| --- | --- | --- |
| Valid | enabled as explicitly granted | fully functional |
| Grace | enabled; operator warning reason exposed | fully functional |
| Expired | disabled | Community remains functional; all data readable |
| Invalid/malformed/revoked | disabled | Community remains functional; all data readable |
| No license | disabled | normal Community mode |

Expiry or invalidity must never delete, encrypt again, lock, corrupt, or make
user data unreadable. Replacing a license is non-destructive.

## Installation binding

Migration 9 creates one random `installation_id` row in PostgreSQL. The ID is
stable and available to authenticated workspace administrators at
`GET /api/v1/installation` for license requests.

- normal PostgreSQL backup/restore preserves the ID and license validity;
- a byte-for-byte clone also preserves the ID and is the same licensed
  identity, not an automatically licensed second installation;
- a fresh database creates a new ID and an old license fails binding;
- disaster recovery from the authoritative backup keeps the ID;
- reissue is required for an intentionally new installation, lost identity
  row, or independent active clone.

There is no clone-detection bypass. Contractual/operational controls must
prevent concurrent use of one identity as multiple installations. See the
[clone and disaster-recovery matrix](installation-binding-dr.md).

The issuer boundary and commands are specified in the
[offline issuer architecture](license-issuer-architecture.md). No customer
license or production private key exists in this repository.

## Configuration

Set `ENTERPRISE_LICENSE_PATH` to a read-only license file. Helm can mount a
Secret with `enterprise.licenseSecretName`; Compose/host deployments can mount
the file directly. `ENTERPRISE_SERVICE_URL` is optional and does not affect
Community availability.
