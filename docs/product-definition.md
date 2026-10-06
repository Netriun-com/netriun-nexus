# Netriun Nexus product definition

This document is the product contract for Netriun Nexus. Product, UX, API,
licensing, and deployment decisions should remain consistent with it. Changes
to this contract require an explicit product decision and a matching update to
the roadmap.

## Product promise

Netriun Nexus is a multi-tenant cloud operations control plane. It gives a team
one workspace for cloud inventory, safe lifecycle operations, access control,
audit history, and cost reporting across multiple providers.

Nexus is delivered from one stable public core contract in three forms. The
Community image contains only the AGPL Open Source Work; Enterprise and Cloud
compose it with separately built proprietary components:

| Form | Operator | Entitlement source | Intended use |
| --- | --- | --- | --- |
| Nexus Cloud | Netriun | Netriun control plane | Public core plus private managed-service infrastructure |
| Nexus Community | Customer | Built-in free entitlement | Public AGPL self-hosted image |
| Nexus Enterprise | Customer | Signed offline license | Public core plus licensed proprietary component(s) |

Community is a usable product, not a time-limited trial. Expiry or absence of an
Enterprise license must never make customer data unreadable.

## Initial edition contract

The following limits are beta defaults and are represented by stable machine
keys so that values may change without rewriting feature checks.

| Capability | Community | Enterprise |
| --- | --- | --- |
| Workspaces | 1 | Licensed limit |
| Human identities | Owner plus 5 members | Licensed limit |
| Cloud accounts | 5 | Licensed limit |
| Inventory and core lifecycle actions | Included | Included |
| Built-in Viewer, Operator, and Account Manager roles | Included | Included |
| Custom access roles/policies | Not included | `custom_access_roles` |
| OIDC and SAML SSO | Not included | `sso` |
| Billing and custom cost reports | Not included | `billing_reports` |
| Excel/PDF billing export | Not included | `billing_export` |
| Scheduled reports | Not included | `scheduled_reports` |
| Audit retention | 30 days | Licensed limit |
| HA deployment guidance | Not included | `ha_guidance` |

Stable limit keys are `workspaces`, `human_identities`, `cloud_accounts`, and
`audit_retention_days`. Stable feature keys must be used by the backend, UI,
API responses, license payload, tests, and documentation.

## Product rules

1. Authorization and entitlement are different decisions. RBAC answers whether
   a person may perform an action; entitlement answers whether the installation
   includes the capability. Both checks are enforced by the backend.
2. A disabled paid capability remains visible where useful, with an explanation
   of the required edition. It must not fail as an unexplained permission error.
3. Invalid, absent, or expired licenses fall back safely to Community behavior.
4. License expiry has a 14-day grace period. During and after expiry, existing
   data remains readable and exportable where the Community contract permits it.
5. Customer cloud credentials and inventory stay inside the selected deployment
   boundary. License validation must not require sending them to Netriun.
6. Cloud and self-hosted installations share versioned core migrations and API
   contracts. They do not have to use the same artifact: proprietary code must
   not be published inside the Community image.

## Current scope

The current product supports workspace registration and verification, local and
federated identity, account-scoped access policy, audit history, snapshot-first
inventory, lifecycle operations, and selected services for AWS, Alibaba Cloud,
Azure, and Google Cloud. Provider depth will grow incrementally; the entitlement
model must not encode provider-specific commercial assumptions.

## Decisions still requiring explicit approval

- The Enterprise commercial license, documentation license, trademark policy,
  and contributor agreement. The Open Source Work is now AGPL-3.0-only with
  explicit Enterprise and Cloud exclusions.
- The Redis server version/license used by the Community distribution; see the
  [dependency license audit](dependency-license-audit.md).
- Final commercial prices and Enterprise limit values.
- The customer/license issuing workflow and support terms.

Implementation details are defined in [the self-hosted specification](self-hosted-spec.md).
