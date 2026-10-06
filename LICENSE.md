# Netriun Nexus licensing boundary

Netriun Nexus uses an open-core model. The license in [`LICENSE`](LICENSE)
is the **GNU Affero General Public License version 3 only
(`AGPL-3.0-only`)**. It applies only to the Open Source Work identified below.
It does not license Netriun Enterprise or Netriun Cloud implementations.

This file defines repository scope; it does not modify the AGPL or add a
field-of-use restriction.

## Open Source Work — AGPL-3.0-only

The following current paths form the Open Source Work:

- `cmd/nexus/**` and `cmd/migrate-legacy/**`
- `internal/app/**`, except excluded brand assets described below
- `internal/cloud/**`
- `internal/entitlements/**`
- `internal/licensing/**` and `internal/enterprise/**`
- `internal/secure/**`
- `scripts/**`
- `deploy/**`
- `.github/**`
- `Dockerfile`, `Makefile`, `compose.yaml`, `go.mod`, and `go.sum`
- `docs/openapi.json`
- `api/**`, plus the boundary manifests in `core/`, `community/`, `providers/`,
  and `cli/`

These paths map to the target architecture as documented in
[`docs/licensing-architecture.md`](docs/licensing-architecture.md). A file in
the Open Source Work without an individual SPDX header is still covered by
this repository-level grant.

## Explicitly outside the AGPL grant

- `enterprise/**`: reserved for proprietary/commercial components.
- `cloud/**`: reserved for proprietary Netriun Cloud infrastructure and
  service components.
- Netriun and Netriun Nexus names, logos, and other brand assets, including
  `internal/app/web/logo.svg`. The AGPL software grant does not grant
  trademark rights.
- Documentation other than `docs/openapi.json`. A documentation license has
  not yet been selected.

The current public repository contains boundary manifests, not proprietary
Enterprise or Cloud implementation. Proprietary source must not be copied
into an AGPL-covered path. A file under `enterprise/` or `cloud/` is not
licensed merely because it is stored in the same repository; it requires an
explicit commercial notice or license supplied by Netriun.

## Network use and commercial use

AGPL-3.0-only permits commercial use, modification, redistribution, and
operation as a network service, subject to its conditions. In particular,
section 13 requires an operator that modifies the covered program and lets
users interact with it over a network to offer those users the Corresponding
Source of that modified version.

The Open Source Work therefore cannot carry an additional restriction that
forbids competing SaaS use while remaining AGPL/Open Source. Netriun Cloud is
protected by keeping its implementation, operational automation, private
configuration, credentials, customer data, and commercial services outside
the Open Source Work. Trademark rights are separate from the copyright
license.

## Contributions, copied code, and generated code

- New first-party source in an Open Source path must use
  `SPDX-License-Identifier: AGPL-3.0-only` where the file format permits it.
- Do not copy third-party code until its exact license and required notices
  are recorded and confirmed compatible with AGPL-3.0-only.
- Generated files must identify their generator and source license, or be
  covered by an explicit path-level license statement when their format does
  not permit comments.
- Provider implementations contributed to `providers/` or the current
  `internal/cloud/` path must be AGPL-3.0-only or under a license that permits
  combination and redistribution with AGPL-3.0-only.
- A proprietary Enterprise or Cloud component must communicate through a
  stable boundary and must not copy or statically incorporate AGPL code
  without legal review.

Third-party components retain their own licenses. See [`NOTICE`](NOTICE) and
[`docs/dependency-license-audit.md`](docs/dependency-license-audit.md).

## No legal advice

This repository documents the project's intended licensing boundary. It is
not legal advice. Questions about a future proprietary plugin mechanism,
contributor agreement, trademark policy, or documentation license require a
decision by Netriun and, where appropriate, qualified counsel.
