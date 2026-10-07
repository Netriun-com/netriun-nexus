# Netriun Nexus Helm chart

By default, this chart deploys only the Nexus application and expects external
PostgreSQL and Valkey services. For the current local cluster,
`values-local.yaml` also deploys single-replica PostgreSQL and Valkey StatefulSets
with retained host-path volumes on `k8s-node01`.

Valkey stores short-lived asynchronous compute/EDS/OSS/security-group refresh-job state and
per-account/service collector leases. Keep Valkey available during application rollouts;
the latest inventory snapshot remains in PostgreSQL if an in-flight job is lost.

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
compatibility configuration. To install an offline license, create a separate
Secret containing `license.json` and set `enterprise.licenseSecretName`. The
license is mounted read-only; no private signing key belongs in Kubernetes.

## Optional Enterprise service authentication

Community does not require the proprietary Enterprise service. Leave
`enterprise.serviceURL` empty to keep it disabled. When it is enabled, Core
accepts only HTTPS and uses TLS 1.3 mutual authentication.

For Kubernetes, install and attest SPIRE separately, then configure the exact
Enterprise SPIFFE ID. The Workload API socket is mounted read-only from the
node; private workload keys are delivered and rotated by SPIRE rather than
stored in a Kubernetes Secret:

```yaml
enterprise:
  serviceURL: https://nexus-enterprise:8443
  auth:
    mode: spiffe
    spiffe:
      endpointSocket: unix:///run/spire/sockets/agent.sock
      socketHostPath: /run/spire/sockets
      serverID: spiffe://netriun.com/ns/nexus-nteriun/sa/nexus-enterprise
```

For local/self-hosted deployments without SPIRE, set `auth.mode: files` and
provide an installation-local CA plus a Core client certificate in a Secret.
The certificate files are mounted read-only. Do not reuse a CA or private key
between installations:

```sh
kubectl create secret generic nexus-enterprise-mtls \
  --from-file=ca.crt=/secure/path/ca.crt \
  --from-file=tls.crt=/secure/path/core.crt \
  --from-file=tls.key=/secure/path/core.key \
  --namespace nexus-nteriun
```

```yaml
enterprise:
  serviceURL: https://nexus-enterprise:8443
  auth:
    mode: files
    files:
      secretName: nexus-enterprise-mtls
      serverName: nexus-enterprise
```

The chart creates an ingress NetworkPolicy for Enterprise-labelled Pods and
allows only Core Pods from this Helm release to reach the configured port. The
policy is defense in depth and does not replace mTLS identity checks. The
Enterprise Service itself is a separate proprietary deployment and must remain
ClusterIP-only with no Ingress or NodePort.

For non-Kubernetes Compose installations, use
`deploy/docker/compose.enterprise-mtls.example.yaml` as an explicit override;
it mounts an installation-local certificate directory read-only and keeps the
private key outside the repository and image.

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
