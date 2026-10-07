# Enterprise process architecture

Status: M4 technical boundary approved and implemented in Core contracts.

## Product boundary

```text
AGPL Community/Core
        |
        | HTTP/JSON /enterprise/v1
        | independently versioned DTOs
        v
Proprietary Enterprise Service
        |-- offline license verification policy
        |-- proprietary feature implementations
        `-- private storage and private migrations
```

Process separation is a technical coupling boundary, not by itself a legal
guarantee that a component is outside the AGPL. Distribution, derivative-work,
and network-interaction facts still require qualified legal review. The design
avoids making a legal conclusion from process topology.

## Core remains AGPL

Existing AGPL implementations stay in Core: local authentication and sessions,
workspace/users, baseline OIDC and SAML, JIT and group mapping, billing
ingestion and existing billing reports, PDF/XLSX export, RBAC/custom roles,
resource overview, basic reports, health/readiness, Community entitlements,
and basic deployment functionality.

New proprietary capability identifiers are limited to:

- `advanced_sso`
- `identity_governance`
- `cost_management`
- `custom_report_builder`
- `scheduled_reports`
- `policy_automation`
- `ha_operations`

An Enterprise implementation must not replace, relabel, or make the existing
Core implementations unavailable.

## Contract rules

The public DTOs live in `api/enterprise/v1`; Core's optional HTTP client lives
in `internal/enterprise`. Version 1 uses `/enterprise/v1`, exposes supported
major versions from `/enterprise/v1/info`, and rejects an incompatible major.

- no static or dynamic linking to proprietary implementation code;
- no import from Core `internal` packages by Enterprise;
- no direct Enterprise access to Core PostgreSQL tables;
- no shared migration directory or ownership of Core tables;
- independent JSON DTOs rather than serialized internal structs;
- an installation/workspace/request context, not database handles;
- authentication for the Core-to-Enterprise channel is required before a
  production Enterprise service is connected;
- Core startup, health, login, data reads, and Community mutations must work
  with no Enterprise URL and during Enterprise outages.

Enterprise storage and migrations belong to the proprietary service. Data
created by a proprietary capability must remain exportable/readable through a
defined degradation path after expiry; license state must never delete it.

## Capability failure behavior

Core performs RBAC and entitlement checks before invoking a proprietary
capability. Missing, incompatible, timed-out, or unhealthy Enterprise service
responses fail only that Enterprise operation with a retryable service error.
They do not make Core unready. No generic remote operation may be used to
bypass the enumerated capability allowlist.

The M5 [service-authentication recommendation](enterprise-service-authentication.md)
selects mTLS workload identities, ClusterIP-only exposure and NetworkPolicy as
defense in depth, subject to owner approval before implementation. Mutual
authentication, authorization and replay/idempotency controls are release
blockers for a proprietary feature service, not for Community.
