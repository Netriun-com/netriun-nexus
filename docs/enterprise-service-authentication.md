# Core to Enterprise service authentication

Status: M5 architecture approved. The Core client, TLS 1.3 mTLS modes,
request-integrity headers and Enterprise ingress NetworkPolicy are implemented.
The current cluster has no SPIRE or Enterprise service, so live X.509-SVID
issuance/rotation and server-side idempotency conformance remain deployment
blockers for the first proprietary service—not for Community.

## Options considered

| Mechanism | Strengths | Risks / operational cost | Decision |
| --- | --- | --- | --- |
| mTLS with workload identities | Mutual service identity, confidentiality, short-lived credentials, works without bearer secrets | Needs CA/rotation and local-install provisioning | **Recommended** |
| OAuth2 client credentials / JWT | Familiar authorization and proxy support | Bearer-token replay and an issuer dependency; adds token lifecycle | Not primary |
| Shared HMAC secret | Simple for small installs | Symmetric secret distribution, rotation and replay protocol become custom | Rejected as primary |
| NetworkPolicy only | Useful blast-radius reduction | Identifies network locations, not cryptographic callers | Defense in depth only |

## Selected design

Use TLS 1.3 mutual authentication with explicit workload identity
authorization. On Kubernetes, prefer short-lived X.509-SVIDs delivered by
SPIRE when it is installed. Core accepts only the Enterprise server SPIFFE ID;
Enterprise accepts only the Core client SPIFFE ID. A deployment without SPIRE
uses the same mTLS protocol with an installation-local CA and separately
mounted client/server certificates. Static long-lived bearer tokens are not a
fallback.

For local/self-hosted deployments, bind Enterprise to loopback or a private
interface, require the same mTLS client certificate, pin the installation CA,
and keep certificate keys in owner-readable files outside images and Compose
files. Certificate renewal must overlap trust roots and fail closed for
Enterprise calls without affecting Community operation.

Each mutating request should additionally contain a request ID, canonical body
digest and signed/transport-authenticated issuance timestamp. Enterprise keeps
a bounded idempotency/replay record in its private store: retrying the same ID
and digest returns the recorded result; the same ID with another digest is
rejected; stale requests are rejected. This protects application semantics
beyond TLS packet replay protection.

## Kubernetes topology

- Enterprise uses an internal `ClusterIP` only, without Ingress, NodePort or
  LoadBalancer.
- Default-deny ingress selects Enterprise pods; the sole allow rule selects
  Core pods in the approved namespace on the Enterprise TLS port.
- The current chart isolates Enterprise ingress and allows only Core on its TLS
  port. A general Core default-deny egress policy is intentionally not claimed:
  cloud-provider and SMTP destinations can be dynamic, so their allowlist and
  CNI/FQDN capabilities need a separate deployment design. Because Kubernetes
  NetworkPolicy is additive and depends on CNI enforcement, deployment tests
  must prove both allowed and denied probes on the target cluster.
- NetworkPolicy is not service authentication; mTLS authorization remains
  mandatory.
- service-account token automount remains disabled unless SPIRE integration
  specifically requires a projected, audience-bound token for attestation.

## Required tests after approval

- valid Core identity succeeds and the server identity is verified;
- missing, external, wrong-namespace and wrong-SPIFFE identities fail;
- expired/untrusted certificates fail;
- incompatible API major and Enterprise outage affect only Enterprise calls;
- duplicate request ID/same digest is idempotent;
- duplicate request ID/different digest and stale timestamp fail;
- NetworkPolicy allows Core and rejects an unrelated probe pod.

## Request-level contract

Every capability mutation carries:

- `X-Netriun-Request-ID` and the identical `Idempotency-Key`;
- canonical UTC `X-Netriun-Request-Timestamp`, accepted within five minutes;
- `Content-Digest` containing SHA-256 of the exact transmitted JSON bytes;
- the installation, workspace and request IDs in the versioned request body.

The Enterprise service must bind this metadata to the authenticated Core
SPIFFE ID/certificate. Its private idempotency store returns the recorded result
for the same request ID and digest, rejects the same ID with another digest,
and rejects stale timestamps. Core maps authentication failures and conflicting
replays to explicit errors while keeping Community healthy.

## Core configuration

`ENTERPRISE_SERVICE_URL` must be HTTPS. With `ENTERPRISE_AUTH_MODE=spiffe`,
Core reads short-lived X.509-SVIDs from `SPIFFE_ENDPOINT_SOCKET` and authorizes
only `ENTERPRISE_SPIFFE_SERVER_ID`. With `ENTERPRISE_AUTH_MODE=files`, Core
requires a CA, client certificate/private-key pair and exact server name from
mounted read-only files. Both modes require TLS 1.3.

If the Enterprise URL is empty, no identity source is initialized. If an
optional Enterprise identity source is unavailable or misconfigured, Core logs
the failure, leaves Enterprise disabled and continues in Community mode.

For Compose, set `ENTERPRISE_SERVICE_URL`, `ENTERPRISE_MTLS_DIR` and
`ENTERPRISE_MTLS_SERVER_NAME`, then add the checked-in override explicitly:

```sh
docker compose \
  -f compose.yaml \
  -f deploy/docker/compose.enterprise-mtls.example.yaml \
  up -d
```

The mounted directory must be outside the repository, owner-readable only,
and contain `ca.crt`, `core.crt` and `core.key`. Renewal should issue the new
client certificate before the old certificate expires, atomically replace the
mounted files, and restart Core; CA rotation must use an overlap period during
which the Enterprise server trusts both old and new installation-local roots.
