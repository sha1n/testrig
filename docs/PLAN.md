# Build Plan

This document tracks feature delivery for the initial build-up of `testrig-go`. Each item is a single commit. Tick a box when the corresponding commit lands on `master`.

## Status

- [x] **chore: initial repo setup** — Makefile, `.gitignore`, basic CI (build + test), monthly Dependabot, minimal README, empty `go.mod`.
- [x] **docs: add project specs and task plan** — `docs/SPEC.md` (target-state public API & semantics) and this file.
- [x] **feat: add `internal/dag` for dependency graph validation** — cycle detection used by `Env`.
- [x] **feat: add core types (`Service`, `Properties`, `TestEnvContext`)** — interface layer with no runtime; `pkg/testrig/testrig.go`.
- [x] **feat: add `DiscoveryStore` (`MapStore`, `OsEnvStore`)** — pluggable storage backends for discovery (lands before Env so the latter compiles).
- [x] **feat: add `Env` with reactive Start/Stop lifecycle** — concurrent dependency-aware orchestration; envDiscovery providers; `internal/testutil` for shared test helpers.
- [x] **feat: add `InjectIntoEnv` helper** — `t.Setenv`-based property → env-var injection.
- [x] **feat: add postgres testkit** — testcontainers-go PostgreSQL Testkit (implements testrig.Service).
- [x] **feat: add wiremock testkit** — testcontainers-go WireMock Testkit (implements testrig.Service).
- [ ] **feat: add `viper-app` example** — config-injection demo using Viper.
- [ ] **feat: add `koanf-app` example** — config-injection demo using koanf.
- [ ] **docs: expand README with usage and feature overview** — final polish; release-ready public docs.

## Out of Scope (planned separately)

- Full CI: `make lint` step (golangci-lint via Go `tool` directive), `make coverage` upload, release tagging, multi-Go matrix.
- `LICENSE`.
- Codecov badge (re-add when coverage CI lands).
- Branch protection on `master` (manual GitHub setting).
