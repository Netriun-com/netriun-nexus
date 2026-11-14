# Netriun Nexus Helm chart

By default, this chart deploys only the Nexus application and expects external
PostgreSQL and Valkey services. For the current local cluster,
`values-local.yaml` also deploys single-replica PostgreSQL and Valkey StatefulSets
with retained host-path volumes on `k8s-node01`.

Compute refresh jobs, polling receipts and leases live in PostgreSQL and survive
application rollouts. Valkey still stores sessions and short-lived EDS/OSS/security-group
refresh state and service leases; keep it available during rollouts.

Create the application Secret before installation. Do not commit literal
credentials to a values file:

```sh
kubectl create secret generic netriun-nexus \
  --from-literal=DATABASE_URL='postgres://...' \
  --from-literal=REDIS_URL='redis://...' \
  --from-literal=ENCRYPTION_KEY='...' \
  --from-literal=ADMIN_USERNAME='admin' \
  --from-literal=ADMIN_EMAIL='admin@example.com' \
  --from-literal=ADMIN_PASSWORD='...' \
  --from-literal=SMTP_USERNAME='sender@example.com' \
  --from-literal=SMTP_PASSWORD='...'
```

SMTP server, port, sender address, and sender display name are non-secret chart
values under `config.smtp`. Keep the SMTP app password only in the existing
Kubernetes Secret.

When the bundled local PostgreSQL and Valkey instances are enabled, the same
Secret must also contain `POSTGRES_PASSWORD` and `REDIS_PASSWORD`.

The `REDIS_URL`/`REDIS_PASSWORD` names are retained as wire-protocol
compatibility configuration.

Nexus currently runs with Community entitlements in both deployment modes.
Offline license, Enterprise service, mTLS, and SPIFFE runtime settings and mounts
have been removed from this chart. Existing `enterprise.serviceURL`,
`enterprise.licenseSecretName`, and `enterprise.auth` overrides no longer affect
Nexus. Remove obsolete overrides and Enterprise variables from the application
Secret when upgrading; the runtime ignores them.

The separate `enterprise.networkPolicy` values and template are retained until
T8 moves that legacy deployment artifact to the shared licensing project. They
do not configure a Nexus runtime client. The legacy mTLS Compose example is
also retained for that extraction and is no longer a supported Nexus override.

## Process roles and optional worker Deployment

`worker.enabled` defaults to `false`: the existing Deployment uses
`NEXUS_ROLE=all` and runs HTTP plus the durable worker pool. With
`worker.enabled=true`, that Deployment uses `NEXUS_ROLE=serve` and a separate
`-worker` Deployment uses `NEXUS_ROLE=worker`. The runtime rejects any other
role; outside Helm, an unset role defaults to `all`. Compose remains combined.

```sh
helm lint deploy/helm/netriun-nexus --set worker.enabled=true
helm template nexus deploy/helm/netriun-nexus --set worker.enabled=true
```

The worker uses the same image, ConfigMap, Secret, service account, and pod
security settings. `worker.replicaCount` defaults to 1 and `worker.resources`
controls its resource requests/limits. Both modes share these settings:

| Helm value | Runtime variable | Default |
| --- | --- | --- |
| `worker.concurrency` | `NEXUS_WORKERS` | `8` goroutines per process |
| `worker.accountConcurrency` | `NEXUS_ACCOUNT_CONCURRENCY` | `2` jobs per `account:<id>` key |
| `worker.providerConcurrency` | `NEXUS_PROVIDER_CONCURRENCY` | `4` jobs per `provider:<name>` key |
| `worker.shutdownTimeoutSeconds` | `NEXUS_SHUTDOWN_TIMEOUT` | `15` seconds |

Per-key limits apply across all worker processes and must match between replicas.
Compute jobs consume both account and provider limits across workers. Other job
kinds retain their existing single-key behavior. Unknown kinds remain queued.
Scheduled sync runs in worker/all roles. Manual compute refresh uses the same
durable jobs; EDS/OSS/security-group runners migrate in T14.

Worker startup, liveness, and readiness probes use `GET /healthz` on its pod's
HTTP port. It reports whether the worker runtime is active, not whether the
database or cloud providers are reachable. The worker has no application API or
`/readyz`. The web Deployment retains its existing `/readyz` and `/healthz`
probes. Worker pods use a distinct name label so the existing immutable web
selector, Service, and web PodDisruptionBudget do not select them; enabling or
disabling the worker does not change the web Deployment selector.

SIGTERM stops claims and allows active handlers to finish for the configured
drain timeout. Leases are renewed every 10s with a 30s lifetime. At the drain
deadline renewal stops, leaving unfinished jobs for recovery after lease expiry.
The chart requires `terminationGracePeriodSeconds` to be at least
`max(15, worker.shutdownTimeoutSeconds) + 5` (default 30). The HTTP shutdown and
worker drain budgets run concurrently. See the [runtime documentation](../../../README.md#process-roles-and-durable-workers)
for handler requirements and retry behavior.

## Scheduled collection settings

Workers now need DATABASE_URL, REDIS_URL, and ENCRYPTION_KEY from the existing
Secret. EDS/OSS/security-group collectors still need Valkey coordination; compute
uses queue leases and transactional write fencing. The scheduler never runs in serve mode.

All `sync` values are positive integer seconds, rendered as Go durations:

| Helm value | Environment variable | Default seconds |
| --- | --- | --- |
| `sync.computeIntervalSeconds` | `NEXUS_SYNC_COMPUTE_INTERVAL` | 300 |
| `sync.desktopsIntervalSeconds` | `NEXUS_SYNC_DESKTOPS_INTERVAL` | 300 |
| `sync.usersIntervalSeconds` | `NEXUS_SYNC_USERS_INTERVAL` | 300 |
| `sync.securityGroupsIntervalSeconds` | `NEXUS_SYNC_SECURITY_GROUPS_INTERVAL` | 900 |
| `sync.bucketsIntervalSeconds` | `NEXUS_SYNC_BUCKETS_INTERVAL` | 3600 |
| `sync.emptyIntervalSeconds` | `NEXUS_SYNC_EMPTY_INTERVAL` | 21600 |
| `sync.tickIntervalSeconds` | `NEXUS_SYNC_TICK_INTERVAL` | 10 |
| `sync.reconcileIntervalSeconds` | `NEXUS_SYNC_RECONCILE_INTERVAL` | 60 |
| `sync.discoveryIntervalSeconds` | `NEXUS_SYNC_DISCOVERY_INTERVAL` | 3600 |
| `sync.jobRetentionSeconds` | `NEXUS_JOB_RETENTION` | 604800 |

Use the same settings for web/account saves and every worker replica. The `*`
region sentinel denotes account-wide Azure/GCP compute and Alibaba OSS, security
groups, and EDS users. These scopes retain their service interval and never use
the six-hour empty-region delay. AWS/Alibaba compute and EDS desktops have
regional rows; all-region accounts need successful worker region discovery.
Database reconciliation backfills existing accounts after upgrade and preserves
disabled rows. Provider region discovery has persisted hourly due times per
account and a PostgreSQL advisory lock shared across replicas; due-time polling
uses the reconciliation interval. New accounts need successful discovery before
manual compute refresh can enqueue their regional scopes. Cleanup deletes only
old terminal jobs, in batches of 500.

TODO(T14): replace Alibaba account-wide rows once collectors are regional.
EDS users may also refresh desktops, but does not update desktop sync_state rows.
Compute refresh uses durable jobs and the existing polling response shape;
EDS/OSS/security-group runners and page sync triggers remain unchanged. See
[scheduled sync](../../../README.md#scheduled-sync) for deduplication, ownership,
error handling, empty-region behavior, and transitional snapshot limitations.

Render and inspect without deploying:

```sh
helm lint deploy/helm/netriun-nexus
helm template nexus deploy/helm/netriun-nexus \
  --set config.appOrigin=https://nexus.example.com
```

Deploy the local-cluster profile:

```sh
helm upgrade --install nexus deploy/helm/netriun-nexus \
  --namespace nexus-nteriun \
  --values deploy/helm/netriun-nexus/values-local.yaml \
  --wait
```

The local profile deliberately does not create an Ingress. HTTPS terminates at
Cloudflare, while Cloudflare Tunnel connects to this in-cluster HTTP origin:

```text
http://nexus-service.nexus-nteriun.svc.cluster.local:80
```

Create a Cloudflare Tunnel public hostname for `nexus.netriun.com` using that
origin. Nexus itself is configured with `APP_ORIGIN=https://nexus.netriun.com`
and secure cookies, so the public application remains HTTPS-only.

The bundled databases are appropriate for this local first deployment, but
their host-path storage is tied to `k8s-node01`. Back up
`/var/lib/netriun-nexus` on that node and move to replicated or managed storage
before treating the cluster as highly available.

The former `nexus-redis` PVC is intentionally retained during the Valkey
rollback window. See [the migration record](../../../docs/redis-licensing.md)
before rollback or cleanup.
