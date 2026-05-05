# Build Plan

This document tracks feature delivery for the initial build-up of `testrig`. Each item is a single commit. Tick a box when the corresponding commit lands on `master`.

> Historical-accuracy note: descriptions below preserve the names that were in effect when each commit landed (e.g. "Testkit" type, `pkg/testrig/...` path, module `testrig-go`). A subsequent restructure flattened the layout to module root, renamed types to `postgres.Postgres` / `wiremock.WireMock`, and renamed the module to `github.com/sha1n/testrig`. See `docs/SPEC.md` for the current public surface.

## Status

- [x] **chore: initial repo setup** — Makefile, `.gitignore`, basic CI (build + test), monthly Dependabot, minimal README, empty `go.mod`.
- [x] **docs: add project specs and task plan** — `docs/SPEC.md` (target-state public API & semantics) and this file.
- [x] **feat: add `internal/dag` for dependency graph validation** — cycle detection used by `Env`.
- [x] **feat: add core types (`Service`, `Properties`, `EnvContext`)** — interface layer with no runtime; `pkg/testrig/testrig.go`.
- [x] **feat: add `DiscoveryStore` (`MapStore`, `OsEnvStore`)** — pluggable storage backends for discovery (lands before Env so the latter compiles).
- [x] **feat: add `Env` with reactive Start/Stop lifecycle** — concurrent dependency-aware orchestration; envDiscovery providers; `internal/testutil` for shared test helpers.
- [x] **feat: add `SetEnvVars` helper** — `t.Setenv`-based property → env-var injection.
- [x] **feat: add postgres testkit** — testcontainers-go PostgreSQL Testkit (implements testrig.Service).
- [x] **feat: add wiremock testkit** — testcontainers-go WireMock Testkit (implements testrig.Service).
- [x] **feat: add `viper-app` example** — config-injection demo using Viper.
- [x] **feat: add `koanf-app` example** — config-injection demo using koanf.
- [x] **docs: expand README with usage and feature overview** — final polish; release-ready public docs.
- [x] **refactor: rename module, flatten layout, drop "Testkit" vocabulary** — module → `github.com/sha1n/testrig`; `pkg/testrig/*` → module root; `pkg/testrig/testkits/*` → `services/*`; types `Testkit` → `Postgres` / `WireMock`; `NewCrossProcessDiscovery` → `NewOsEnvDiscovery`; identifier hashes drop Name; liveness check respects ctx; SPEC rescoped for honest cross-process semantics; Go floor lowered to 1.24.

## Out of Scope (planned separately)

- Full CI: `make lint` step (golangci-lint via Go `tool` directive), `make coverage` upload, release tagging, multi-Go matrix.
- `LICENSE`.
- Codecov badge (re-add when coverage CI lands).
- Branch protection on `master` (manual GitHub setting).
