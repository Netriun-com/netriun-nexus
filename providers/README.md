# Provider boundary

`providers/` is the target home for cloud-provider adapters and provider data
models. First-party implementations are AGPL-3.0-only; third-party provider
code must use an AGPL-compatible license and preserve its notices.

Current implementation: `internal/cloud` plus provider-specific orchestration
handlers that are still located in `internal/app`.
