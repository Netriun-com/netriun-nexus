# Open-core licensing architecture

Status: approved M3 boundary, 2026-10-06.

Netriun Nexus separates the AGPL Open Source Work from proprietary Netriun
Enterprise and Netriun Cloud implementations. The authoritative repository
scope is [`../LICENSE.md`](../LICENSE.md); this document describes the package
architecture and migration path.

## Target shape

```text
netriun-nexus/
├── core/          # AGPL-3.0-only, provider-neutral domain and policy
├── community/     # AGPL-3.0-only, self-hosted Community composition
├── providers/     # AGPL-3.0-only or AGPL-compatible provider adapters
├── api/           # AGPL-3.0-only, public transports and contracts
├── cli/           # AGPL-3.0-only, commands and operator tools
├── docs/          # license pending, except OpenAPI as noted in LICENSE.md
├── enterprise/    # proprietary boundary; no implementation here today
└── cloud/         # proprietary boundary; no implementation here today
```

The target directories initially contain boundary manifests. Moving working
packages is deliberately separated from licensing: a large path-only refactor
would add regression risk without improving the legal boundary. Until those
moves happen, `LICENSE.md` maps every current path explicitly.

## Current package mapping

| Current path/package | Target section | License/status | Notes |
| --- | --- | --- | --- |
| `internal/entitlements` | `core` | AGPL-3.0-only | Edition-neutral feature and limit decisions; future Enterprise providers implement interfaces without changing Community decisions. |
| `internal/secure` | `core` | AGPL-3.0-only | Encryption and password/security primitives. |
| Provider-neutral parts of `internal/app` | `core` | AGPL-3.0-only | Domain orchestration, persistence, authorization, audit, refresh jobs, and SSO contracts must be extracted gradually. |
| `internal/app/web` except `logo.svg` | `community` | AGPL-3.0-only | Embedded Community portal. The logo is a brand asset outside the software grant. |
| `internal/app/migrations` | `community` + `core` persistence | AGPL-3.0-only | Shared schema currently deploys as one product. Enterprise schema extensions must use separate migrations owned by the proprietary module. |
| `internal/cloud` | `providers` | AGPL-3.0-only | AWS, Alibaba, Azure, and GCP adapters. |
| Provider-specific handlers in `internal/app` | `providers` + `api` | AGPL-3.0-only | `alibaba_billing.go`, `eds*.go`, `oss.go`, `security_groups.go`, `collector.go`, and provider branches in instance/account handlers should move behind interfaces. |
| HTTP routes and handlers in `internal/app` | `api` | AGPL-3.0-only | Transport is currently coupled to the application package. |
| `docs/openapi.json` | `api` | AGPL-3.0-only | Machine-readable public API contract; JSON cannot carry a comment header. |
| `cmd/nexus` | `cli` | AGPL-3.0-only | Server entry point and composition root. |
| `cmd/migrate-legacy` | `cli` | AGPL-3.0-only | Offline migration utility. |
| `compose.yaml`, `deploy/`, `scripts/` | `community` distribution | AGPL-3.0-only | Self-hosted packaging and operations. Runtime images keep their own licenses. |
| `.github/`, `Dockerfile`, `Makefile`, Go module files | Open Source build system | AGPL-3.0-only | Builds and verifies the Open Source Work. |
| `docs/` except `openapi.json` | `docs` | Decision pending | No documentation license was selected in M3. |
| `enterprise/` | `enterprise` | Proprietary boundary | Contains only a public boundary manifest. Proprietary source should normally live in a private module/repository. |
| `cloud/` | `cloud` | Proprietary boundary | Contains only a public boundary manifest. Private infrastructure, configuration, and service code stay outside the public source tree. |

## Dependency direction

The stable dependency direction is:

```text
cli/community/api/providers ──> core contracts
enterprise (private)          ──> versioned public extension contracts
cloud (private)               ──> published API/artifact + private operations
```

`core` must never import `enterprise` or `cloud`. Community startup must work
when no proprietary component exists. Enterprise capability is discovered by
an explicit registered implementation and is still authorized through the
same backend entitlement and RBAC decisions.

Because AGPL obligations depend on the actual form of combination and
distribution, this document does not claim that an in-process plugin boundary
automatically keeps a proprietary plugin outside AGPL. Before implementing
Enterprise loading, Netriun must obtain legal review of the chosen boundary.
A separately deployed service using a versioned protocol is the lowest-coupling
default, but it is not a substitute for legal advice.

## Netriun Cloud boundary

The public Open Source Work may be self-hosted and may also be used to operate
a network service under AGPL terms. Netriun Cloud differentiation must remain
outside that Work:

- tenant-control infrastructure and deployment automation;
- production configuration, secrets, credentials, and incident tooling;
- customer data and operational datasets;
- proprietary billing, support, compliance, and fleet services;
- Enterprise components supplied under commercial terms; and
- protected Netriun branding and service identity.

There is no “no competing SaaS” restriction in the AGPL-covered code. Adding
one would contradict the approved Open Source model.

## Source and notice policy

The repository-level license statement covers existing files. New source
files should carry the relevant SPDX identifier when their format allows it.
JSON, generated output, and binary/brand assets need an adjacent path-level
statement. Every copied or generated contribution must record provenance,
license, generator, and required notices before merge.

Third-party dependencies are reviewed at the version actually selected by
`go.mod`/`go.sum`. Container tags must be pinned before a release audit because
a floating tag can change both code and license without a repository diff.

## Decisions intentionally deferred

The following require an explicit Netriun decision and, where noted, legal
review:

1. license for human-authored documentation;
2. legal copyright holder name and contributor/CLA or DCO policy;
3. trademark and brand-asset usage policy;
4. proprietary Enterprise commercial license text;
5. Enterprise process/plugin boundary and distribution model; and
6. Redis runtime version/license choice described in the dependency audit.
