# Netriun Nexus — Product spec v2

Status: approved by Saheb on 2026-10-30.
Source of truth while editing: [Spec v2 in Claude Docs](https://claude.ai/artifact/WM73Ag9AYNmm5UbuYAum9f).
This file is the repository copy that Codex reads. Change both together.

Related: [target architecture](architecture-v2.md), [roadmap](roadmap.md), [task list](../task.md).

## Promise and target users

Nexus is a **multi-cloud account orchestration platform**: it creates, changes,
and manages the network, compute, database, and other resources of every cloud
account in one place, more simply and more visually than each provider's own
console.

The problem it solves: in every provider, a simple intent such as "build a
network" turns into many separate steps (VPC, subnets, route tables, gateways,
security groups, load balancer), and each provider has its own terms and UI.

| User | What they need to do |
| --- | --- |
| Network engineer | Create and change networks, subnets, security groups, load balancers, and connectivity, while seeing the topology |
| Account manager | See every resource across all accounts, manage instances, team access, and cost |
| Operator | Day-to-day operations (start, stop, resize, snapshot) without signing in to several provider consoles |

## Product principles

Every UX, API, and architecture decision must fit these principles:

1. **Intent, not steps:** the user states what they want ("a network with two
   subnets and a load balancer"); Nexus works out which provider resources to
   create and in which order.
2. **Preview before apply:** every change first shows a readable plan: what will
   be created, changed, or deleted, and an estimated cost where possible.
3. **Visual by default:** relationships between resources (which instance sits
   in which subnet, behind which load balancer) are shown as a topology; tables
   are for search and bulk actions.
4. **One model for every provider:** shared concepts (Network, Subnet,
   Instance, Load Balancer, Security Rule) look the same in the UI; the
   provider's own name (for example VPC or VSwitch) is shown next to them.
5. **Data is always instant and stable:** pages read from Nexus's own database,
   sync runs in the background, and changes arrive without a page refresh and
   without flicker.
6. **Safe and traceable:** every operation passes RBAC and access policy, is
   recorded in the audit log before it runs, and a partial failure leaves a
   clear state that can be resumed.
7. **Nexus does not own resources:** the provider is the source of truth. A
   change made directly in a provider console appears on the next sync and
   breaks nothing.

## Core capabilities

| Capability | Today in code (commit `0929f6a`) | v2 |
| --- | --- | --- |
| Accounts and scope | AWS, Alibaba, Azure, and GCP connections; provider and account selection in the sidebar | No fundamental change |
| Resource catalog | VMs in their own table; EDS, OSS, and security groups stored as JSON snapshots | One catalog for all resources, with search, filters, and relationships between resources |
| Network topology | Does not exist | Visual view of networks, subnets, instances, load balancers, and rules per account and region |
| Guided workflows | Only the EDS desktop creation wizard | Multi-resource create and change flows with preview and step-by-step execution |
| Instance management | Start, stop, and reboot for all four providers | Add create, resize, snapshot, and bulk actions |
| Access, SSO, and audit | Account-level RBAC, OIDC and SAML, audit log | Kept; access scope opens up to the service level |
| CLI | Does not exist | Dedicated imperative commands, consistent across all cloud platforms, covering the full range of platform capabilities |

Out of scope for the first v2 release: database management, cross-cloud
connectivity (VPN or peering between providers), and Infrastructure-as-Code
export. These come after the core is stable.

### Provider priority

- **Alibaba and AWS** first, including network workflows.
- **Azure and GCP**: inventory and instance operations only, for now.

## Guided workflow: building a network

Every workflow has four stages: form, preview, step-by-step execution, and the
result on the topology.

1. **Simple form:** the user picks the account, region, CIDR, number of subnets
   (public and private), whether internet access and NAT are needed, and
   whether a load balancer is needed. Safe defaults are pre-filled.
2. **Preview:** Nexus lists the provider resources in creation order. For
   example, on Alibaba: VPC, three VSwitches, NAT Gateway, EIP, security group,
   and SLB. CIDR overlap with existing networks is also checked here.
3. **Execution:** each step is a durable job and the state of every resource is
   shown live. If a step fails, the user can retry, keep the resources already
   created, or clean them up (rollback).
4. **Result:** the new network appears on the topology, and the whole operation
   has one audit record with the details of every step.

The first workflow set (approved):

1. Build a network (above).
2. Create an instance inside an existing network (choosing subnet, security
   group, and image).
3. Add instances to a load balancer.
4. Expose a port or service to the internet (security rule, EIP, and listener in
   one flow).
5. Safely delete a network with all its dependencies (mandatory preview).

## Data freshness contract

Opening or switching pages never starts a sync. This contract is the fix for
today's problem (page flicker and sync conflicts when switching pages quickly).

| Rule | Behavior |
| --- | --- |
| Reads | Every page reads only from the Nexus database and opens in under one second |
| Periodic sync | Runs in the background, with a configurable interval per service (for example, instances more often, billing less often) |
| Manual refresh | Only by button, and only for the account, region, and service the user is looking at |
| After an operation | The changed resource is followed until it reaches its final state (for example, stopping to stopped) |
| Showing changes | Only the rows that changed are updated in the UI; the page is never fully refreshed |
| Transparency | Each list shows the time of the last successful sync and any per-account or per-region error in plain language |
| Fairness | Syncing one large account (for example, Alibaba with every region) does not block other accounts or users |
| Durability | A restart or a new deploy does not lose a running sync or operation |

Default sync intervals are defined in the [target architecture](architecture-v2.md#sync-engine)
and are tuned after measuring real accounts.

## Delivery forms and the licensing boundary

Like Netriun Berth, Nexus has two delivery forms. Licensing is a service shared
by every Netriun platform, the **Netriun Commercial Platform**, built outside
this repository.

| Form | Operated by | Entitlement source |
| --- | --- | --- |
| Self-hosted | Customer | Community by default, or a license from the Netriun Commercial Platform |
| Netriun Enterprise | Netriun | Netriun Commercial Platform |

Boundary rules:

- Nexus only has an interface (`internal/entitlements`): "is this feature
  enabled?" and "what is this limit?". License parsing, signatures, the key
  ceremony, SPIFFE, and the issuer are not inside Nexus.
- If the licensing service is unavailable, Nexus behaves as Community and no
  data becomes unreadable.
- Which workflows belong to Community and which to Enterprise is decided after
  the first workflows are built.

## Changes from the previous spec

The product's center moves from "editions and licensing" to "simple, visual
orchestration". Baseline for comparison: `docs/product-definition.md`,
`docs/self-hosted-spec.md`, and `docs/archive/roadmap-v1.md` at commit `0929f6a`.

| Topic | Decision | Note |
| --- | --- | --- |
| Product promise | Change | From "cloud operations control plane" to "multi-cloud account orchestration" |
| The word "approvals" in the README | Build it: two-person approval for large changes | Not in the code yet; keep the README wording only once it ships |
| Access policy, SSO, audit | Keep | The current design is sound |
| Rule that RBAC and entitlement are separate | Keep | |
| Snapshot-first inventory | Change | Becomes "background sync, reads from the database" |
| Offline license, key ceremony, SPIFFE, issuer | Move out of Nexus | Into the Netriun Commercial Platform |
| Edition table and feature keys | Rewrite | After the workflows are defined, with the new feature list |
| Community beta path (old M8 to M10) | Rewrite | After the new core, not before it |
| Alibaba billing report and Excel/PDF export | Keep | With slower freshness (daily sync) |
| EDS wizard | Keep | The first guided workflow example; aligned with the new pattern |

## Decisions (2026-10-30)

- [x] Provider priority: Alibaba and AWS first for network workflows; Azure and
  GCP limited to inventory and instance operations for now.
- [x] First workflow set: the five workflows above.
- [x] Database management comes after the first release.
- [x] Public SaaS (Nexus Cloud with public signup) is not a goal now; revisit
  after the MVP.
- [x] Community versus Enterprise split is decided after the first workflows
  are built.
- [x] Large changes (for example deleting a network) need a second person's
  approval.
- [x] Licensing uses the Netriun Commercial Platform; its API is not defined yet.
