# API boundary

`api/` is the target home for transport contracts, stable error models, and
the public API specification. It is part of the Open Source Work under
AGPL-3.0-only.

Current implementation: HTTP routing/handlers in `internal/app` and
`docs/openapi.json`. The Core-to-Enterprise major-version contract and offline
license schema live in `enterprise/v1`; these contracts are AGPL Core artifacts,
not proprietary implementations. The same directory contains the public
keyring schema; it contains public trust metadata only and no signing key.

`entitlements/v1/netriun-nexus.json` is the product-owned machine-readable
entitlement vocabulary consumed by the separate Commercial Platform. A
published definition version is immutable. Commercial plans may select a
subset or assign limit values, but cannot introduce keys not declared here.
