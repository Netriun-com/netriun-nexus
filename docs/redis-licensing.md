# Valkey runtime and Redis migration

Status: Valkey migration completed on 2026-10-06; legacy Redis storage retained
pending explicit cleanup approval.

As of the M5 deployment, the active Valkey PVC remains healthy on release
`v0.1.35` (Helm revision 35) and the legacy Redis PVC remains bound and unused.
PostgreSQL, Valkey and legacy Redis PVCs were all `Bound` after rollout; Valkey
reported `PONG` and version 8.1.10. Cleanup is safe only after
the owner closes the rollback window, confirms no rollback to the Redis-based
Helm revision is required, preserves or intentionally expires the verified RDB
backup, and explicitly approves deletion. No cleanup is automatic.

## Decision

The Community distribution uses Valkey 8.1.10 instead of Redis 7.4.11.
Valkey is an Open Source, Redis-protocol-compatible server under the
BSD-3-Clause license. The deployed linux/amd64 image is immutable:

```text
valkey/valkey:8.1.10-alpine@sha256:32627109abf6f741121096b45c732f758876803efd7b2e1018ebc0350d117119
```

The Go client remains `github.com/redis/go-redis/v9`; its BSD-2-Clause license
and wire-protocol use are independent from the server implementation.

## Migration evidence

Before cutover, Redis 7.4.11 reported six expiring string keys. A synchronous
RDB snapshot was created, validated with `redis-check-rdb`, copied outside the
repository with mode `0600`, and recorded with this SHA-256:

```text
080602c9ad2065fc5446a4571fc71130e8554ae601c4650e1c6be8625858fda4
```

The snapshot contained six readable keys and a valid checksum. Sessions and
short-lived job/lease state were intentionally not imported into Valkey. No
PostgreSQL copy or migration occurred. Before and after cutover PostgreSQL had
19 public tables, schema migration 8, and a database size of 10,196,659 bytes.

The Kubernetes objects after cutover are:

| Purpose | Workload | PVC | PV/path | Policy |
| --- | --- | --- | --- | --- |
| Active Valkey | `nexus-valkey` | `nexus-valkey` | `nexus-valkey-pv` at `/var/lib/netriun-nexus/valkey` | `Retain` + Helm `keep` |
| Rollback-only Redis data | none | `nexus-redis` | `nexus-redis-pv` at `/var/lib/netriun-nexus/redis` | `Retain` + Helm `keep` |
| PostgreSQL | `nexus-postgresql` | `nexus-postgresql` | unchanged | `Retain` + Helm `keep` |

The compatibility service remains named `nexus-redis`, so existing
`REDIS_URL` secrets do not change; its selector now targets `nexus-valkey`.
A persistence probe wrote a test value, forced a save, deleted the Valkey Pod,
read the value after recreation, and removed the probe value. `/healthz` and
`/readyz` both succeeded afterward.

## Rollback procedure

Do not delete either Valkey or Redis storage before the rollback window closes.
For a rollback:

1. Scale the Nexus deployment to zero and record PostgreSQL readiness.
2. Scale `nexus-valkey` to zero. Do not mount both data directories into one
   server and do not open a Valkey-created data file with Redis 7.4.
3. Roll Helm back to revision 30, or render the corresponding release manifest,
   which recreates `nexus-redis` against the retained `nexus-redis` PVC and
   restores the service selector.
4. Prefer the exact Redis 7.4.11 rollback image
   `redis:7.4.11-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499`
   rather than resolving the former floating `redis:7-alpine` tag.
5. Wait for Redis readiness, then scale Nexus to one and run login, readiness,
   session, refresh-job, and provider smoke tests.
6. If the retained volume is unavailable, restore the verified RDB backup to a
   fresh Redis 7.4.11 volume. Sessions may still require users to sign in again.

Cleanup of `nexus-redis`, `nexus-redis-pv`, and the node path is deliberately
manual and requires explicit owner approval.
