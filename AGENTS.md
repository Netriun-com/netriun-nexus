# Repository guidance

## Product direction and scope

Netriun Nexus is a multi-cloud account orchestration platform. Before working,
read the [product spec](docs/spec-v2.md),
[target architecture](docs/architecture-v2.md), [roadmap](docs/roadmap.md), and
[task list](task.md), including its "Rules for every task" section.

- Work only on tasks in `task.md` and stay within the assigned task.
- Follow the roadmap's phase order and stated exceptions. The v2 architecture
  is a target being implemented incrementally, not a claim that it all exists.

## Branches and pull requests

- Create a new branch from the latest `main`. Never push directly to `main`.
- One task = one small, focused pull request against `main`.
- Describe what changed, how it was tested (commands and results), and anything
  left out or not possible. List unrelated problems in the PR instead of
  fixing them in the same change.
- If acceptance criteria cannot be met, stop and explain why in the PR.
  Never weaken tests or checks, or skip a check to make CI pass.
- When a task is merged, tick it in `task.md` in that PR or a follow-up.

## Required checks

Before opening a PR, run and pass the checks required by `task.md`:

```sh
go build ./...
go vet ./...
TEST_DATABASE_URL='postgres://ccmp:testing-only@localhost:5432/ccmp_test?sslmode=disable' \
TEST_REDIS_URL='redis://localhost:6379/15' \
go test -race ./...
```

- Use the Go version in `go.mod`. Follow [.github/workflows/ci.yml](.github/workflows/ci.yml)
  for PostgreSQL and Valkey images, service health checks, and test configuration.
- Run the tests against isolated test services: `TestIntegration` truncates
  database tables and flushes the selected Valkey database. Use `ccmp_test`
  and Valkey database 15 as CI does; adjust local ports in the URLs if needed.
- Both test URLs must be set so `TestIntegration` actually runs. A run that
  skips it does not satisfy the integration-test requirement.
- The [Makefile](Makefile) provides `make test` (`go test -race ./...`) and
  `make vet` (`go vet ./...`). `make build` builds only `cmd/nexus`, so it
  does not replace the required `go build ./...` check.
- Run any additional acceptance checks specified by the assigned task.

## Architecture and provider APIs

Apply the rules in `docs/architecture-v2.md` to new code; perform existing-code
migrations only as part of their assigned tasks.

- Do not add provider calls to the HTTP read path. New reads use Nexus's own
  stored data; provider work belongs off the read path.
- Put new SQL in a store layer, not in HTTP handlers. `internal/store` is the
  target package; existing SQL still lives largely in `internal/app`.
- Add no new licensing code in this repository. Licensing belongs to the
  external Netriun Commercial Platform; Nexus keeps `internal/entitlements`.
- Add no new code in `internal/licensing` or `internal/enterprise`. Their
  extraction is tracked separately in `task.md`.
- Never guess provider API behavior. Verify it against the provider's official
  documentation and link that documentation in the PR description.

## Documentation and API contracts

- Change code and the documentation describing it in the same PR.
- Update [docs/openapi.json](docs/openapi.json) in the same PR whenever an HTTP
  route, request, or response changes.
- Write all repository documents in English.

## Security

- Preserve the strict Content Security Policy in `internal/app/app.go`,
  including `script-src 'self'`. Do not add CDN scripts or relax the CSP.
- Never put secrets in logs or commits. Keep credentials, tokens, private keys,
  and real environment files out of version control; preserve `.gitignore`'s
  environment-file exclusions.
- Keep stored cloud credentials encrypted at rest through the existing
  `internal/secure` vault, as used by `internal/app/accounts.go`. Do not persist
  plaintext credentials or bypass encryption when saving accounts.
