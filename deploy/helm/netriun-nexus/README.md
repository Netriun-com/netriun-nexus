# Netriun Nexus Helm chart

By default, this chart deploys only the Nexus application and expects external
PostgreSQL and Valkey services. For the current local cluster,
`values-local.yaml` also deploys single-replica PostgreSQL and Valkey StatefulSets
with retained host-path volumes on `k8s-node01`.

All collection jobs, polling receipts and leases live in PostgreSQL and survive
application rollouts. Valkey still stores HTTP sessions and rate limits; keep it available to web pods.

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

## Process roles and separate worker Deployment

`worker.enabled` defaults to `true`: the web Deployment uses `NEXUS_ROLE=serve`
and a separate `-worker` Deployment uses `NEXUS_ROLE=worker`. Set
`worker.enabled=false` to run HTTP plus the worker pool in one `NEXUS_ROLE=all`
Deployment. The runtime rejects any other role; outside Helm, an unset role
defaults to `all`. Compose remains combined. Upgrading with chart defaults now
creates a worker Deployment; budget for its resource requests and limits.

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
All scheduled collection jobs consume both account and provider limits across workers. Other job
kinds retain their existing single-key behavior. Unknown kinds remain queued.
Scheduled sync runs in worker/all roles. Manual compute, EDS, OSS, and security-group refreshes use the same durable jobs.

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

Workers need DATABASE_URL and ENCRYPTION_KEY from the existing Secret.
REDIS_URL is optional and unused in worker mode. The shared Secret may still
contain it for web pods, which require Valkey for sessions and rate limits.
All collectors use queue leases and transactional snapshot fencing.
The scheduler never runs in serve mode.

All `sync` values are positive integer seconds, rendered as Go durations:

| Helm value | Environment variable | Default seconds |
| --- | --- | --- |
| `sync.networkIntervalSeconds` | `NEXUS_SYNC_NETWORK_INTERVAL` | 900 |
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
region sentinel denotes global Azure/GCP compute and Alibaba OSS ListBuckets
(which has no region filter). These scopes never use the six-hour empty delay.
AWS/Alibaba compute and networks, EDS desktops, and security groups have regional rows;
EDS users use the cn-shanghai or ap-southeast-1 directory endpoint scope.
All-region accounts need successful worker region discovery.
Database reconciliation backfills existing accounts after upgrade and preserves
disabled rows. Provider region discovery has persisted hourly due times per
account and a PostgreSQL advisory lock shared across replicas; due-time polling
uses the reconciliation interval. New accounts need successful discovery before
manual compute refresh can enqueue their regional scopes. Cleanup deletes only
old terminal jobs, in batches of 500.

`sync.auditRetentionIntervalSeconds` (default `86400`, emitted as
`NEXUS_AUDIT_RETENTION_INTERVAL`) controls per-workspace audit cleanup. Only
worker/all roles schedule and execute these durable jobs; serve never does.
Each job uses the workspace's `settings.audit_retention_days` and deletes expired
audit rows in batches of at most 500. No cloud accounts or refreshes are needed.

Reconciliation migrates legacy users/security-group `*` scopes and preserves
stored snapshot history. Users jobs no longer refresh desktops. All manual
refreshes use durable jobs and retain their polling contracts; page navigation
never queues sync. Visible service pages poll read-only sync metadata every
30 seconds and patch completed sync results without a page reload. Stop pre-T14
processes before rolling out this version because
they still contain unfenced Valkey runners. Old Valkey refresh IDs are not migrated.
See [scheduled sync](../../../README.md#scheduled-sync) for ownership, errors,
regional migration, and OSS complete-snapshot behavior.

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

## Argo CD on the Netriun staging cluster

The Netriun staging cluster (`k8s-cp01`, `k8s-node01`, `k8s-node02`) runs Nexus
in namespace `nexus` with `values-netriun-k8s.yaml`. Argo CD deploys it from a
release tag of this chart; the Argo CD `Application` and the shared Cloudflare
Tunnel connector live in the platform GitOps repository
(`Netriun-com/netriun-platform`). The public hostname is
`https://nexus-staging.netriun.com`, served through Cloudflare Tunnel with
Cloudflare Access in front.

Create the application Secret in namespace `nexus` before the first sync. Copy
`ENCRYPTION_KEY` byte-for-byte from the previous installation; stored cloud
credentials cannot be decrypted with a different key.

`networkPolicy.enabled` adds ingress NetworkPolicies: web pods accept HTTP only
from the `webIngressFrom` peers (here the `cloudflared` namespace), worker pods
accept no traffic, and the bundled PostgreSQL and Valkey accept only Nexus pods.
Egress is not restricted. Kubelet probes are unaffected.

The bundled PostgreSQL and Valkey PersistentVolumes and claims carry
`argocd.argoproj.io/sync-options: Prune=false,Delete=false` in addition to
`helm.sh/resource-policy: keep`, so pruning or deleting the Argo CD
Application never removes the data volumes. Remove them by hand only after a
verified backup.

```sh
helm lint deploy/helm/netriun-nexus -f deploy/helm/netriun-nexus/values-netriun-k8s.yaml
helm template nexus deploy/helm/netriun-nexus -n nexus \
  -f deploy/helm/netriun-nexus/values-netriun-k8s.yaml
```

Network sync writes VPCs, subnets/VSwitches, and AWS ELBv2 or Alibaba CLB/ALB
resources with provider-ID relations. It requires the additional read-only
permissions in the [network adapter guide](../../../internal/providers/NETWORK.md).
A failed regional collection preserves that region's catalog. Migration 017
adds network scopes and relation ownership; compute and other services keep
their existing intervals and collectors.
