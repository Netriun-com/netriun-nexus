# Core to Enterprise service authentication

Status: M5 recommendation awaiting owner approval; no production Enterprise
service should be connected until this channel is implemented and tested.

## Options considered

| Mechanism | Strengths | Risks / operational cost | Decision |
| --- | --- | --- | --- |
| mTLS with workload identities | Mutual service identity, confidentiality, short-lived credentials, works without bearer secrets | Needs CA/rotation and local-install provisioning | **Recommended** |
| OAuth2 client credentials / JWT | Familiar authorization and proxy support | Bearer-token replay and an issuer dependency; adds token lifecycle | Not primary |
| Shared HMAC secret | Simple for small installs | Symmetric secret distribution, rotation and replay protocol become custom | Rejected as primary |
| NetworkPolicy only | Useful blast-radius reduction | Identifies network locations, not cryptographic callers | Defense in depth only |

## Recommendation

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
- Core egress is restricted to DNS, PostgreSQL, Valkey, explicitly required
  cloud/SMTP endpoints, and the Enterprise service. Because Kubernetes
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

The proposed choice requires owner approval before client/Helm/runtime changes.

