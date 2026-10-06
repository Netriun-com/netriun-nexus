# Staging topology and promotion

Staging validates the same container and Helm chart that will be promoted to a
release. It is not a second development environment and must not reuse production
state.

## Current shared preview

The existing local-cluster deployment remains the product preview while the
self-hosted foundation is built:

| Setting | Value |
| --- | --- |
| Kubernetes namespace | `nexus-nteriun` |
| Helm release | `nexus` |
| Service | `nexus-service` |
| Public hostname | `nexus.netriun.com` |
| Ingress path | Cloudflare Tunnel to in-cluster HTTP service |

This environment is not an isolated release gate. It may contain development
data and credentials and must not be used to validate destructive migrations.

## Target self-hosted staging

Create an isolated environment before the Community beta:

| Setting | Value |
| --- | --- |
| Kubernetes namespace | `nexus-selfhosted-dev` |
| Helm release | `nexus-selfhosted` |
| Service | `nexus-selfhosted` |
| Proposed hostname | `nexus-selfhosted.netriun.com` |
| Image | Immutable GHCR release tag or digest |

It must have separate PostgreSQL, Valkey, Secrets, encryption keys, SMTP test
configuration, persistent volumes, Cloudflare hostname, and cloud test accounts.
No database, Valkey instance, Secret, or persistent volume may be shared with the
current preview.

## Promotion flow

```text
pull request -> CI/race/integration tests -> image build -> vulnerability scan
-> signed GHCR image -> isolated staging -> smoke/upgrade/rollback tests -> release
```

The tested image digest is promoted; staging must not rebuild source. A release
records the application version, chart version, image digest, database migration
level, and rollback notes.

## Staging acceptance checks

1. Fresh install reaches `/readyz` and owner registration/login works.
2. Community limits and premium denials are enforced by API and represented in UI.
3. A valid test license activates only its declared features and limits.
4. Invalid, replaced, grace, and expired licenses follow the failure contract.
5. At least one read-only cloud connection refreshes inventory without blocking navigation.
6. Database backup and restore preserve encrypted credentials and installation ID.
7. Upgrade from the previous release completes without data loss.
8. Rollback procedure is documented and tested against migration compatibility.
9. Logs, audit events, probes, resource limits, and disruption behavior are verified.
10. The exact deployed digest has a passing vulnerability scan and valid signature.

Do not put real customer credentials or production datasets in staging.
