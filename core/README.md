# Core boundary

`core/` is the target home for provider-neutral domain models, entitlement
decisions, authorization primitives, persistence contracts, and security
utilities. It is part of the Open Source Work under AGPL-3.0-only.

Current implementation: `internal/entitlements`, `internal/secure`, and the
provider-neutral parts of `internal/app`. Moving packages is deferred to an
independent refactor so the licensing milestone does not change runtime
behavior.
