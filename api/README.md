# API boundary

`api/` is the target home for transport contracts, stable error models, and
the public API specification. It is part of the Open Source Work under
AGPL-3.0-only.

Current implementation: HTTP routing/handlers in `internal/app` and
`docs/openapi.json`. The Core-to-Enterprise major-version contract and offline
license schema live in `enterprise/v1`; these contracts are AGPL Core artifacts,
not proprietary implementations.
