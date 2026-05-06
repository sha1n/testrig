# Build Plan

This document tracks feature delivery for the initial build-up of `testrig`. Each item is a single commit. Tick a box when the corresponding commit lands on `master`.

> Historical-accuracy note: descriptions below preserve the names that were in effect when each commit landed (e.g. "Testkit" type, `pkg/testrig/...` path, module `testrig-go`, the `Identifier()` method on `Service`, and the discovery layer). Subsequent refactors:
>
> 1. Flattened the layout to module root, renamed types to `postgres.Postgres` / `wiremock.WireMock`, and renamed the module to `github.com/sha1n/testrig`.
> 2. **Dropped the discovery / cross-env reuse subsystem entirely** — the feature could not be made correct under owner/reuser stop coordination, so it was removed. `Service` shrank to 4 methods (no `Identifier`); `WithDiscovery`, `DiscoveryProvider`, `DiscoveryStore`, and the `OsEnvStore`/`MapStore` implementations were deleted.
> 3. Renamed `LifecycleHook.OnStart`/`OnStop` to `AfterStart`/`AfterStop` to reflect their post-event firing semantics. Moved `New`/`MustNew` from `options.go` to `env.go` next to the `Env` type.
> 4. Restored the `WithXxxPropertyName(...)` pattern on the postgres and wiremock services, so testkit outputs can be published directly under application config keys (e.g. `DATABASE_URL`).
>
> See `docs/SPEC.md` for the current public surface.

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
- [x] **refactor: drop discovery, rename hooks, restore property-name customization** — deleted the discovery/reuse subsystem (incoherent owner/reuser coordination); `Service` shrinks to 4 methods (no `Identifier`); `LifecycleHook.OnStart`/`OnStop` → `AfterStart`/`AfterStop`; `New`/`MustNew` moved to `env.go`; postgres/wiremock services regain `WithXxxPropertyName(...)` setters so outputs publish directly under app config keys; examples rebuilt to use the canonical pattern.

## Out of Scope (planned separately)

- Full CI: `make lint` step (golangci-lint via Go `tool` directive), `make coverage` upload, release tagging, multi-Go matrix.
- `LICENSE`.
- Codecov badge (re-add when coverage CI lands).
- Branch protection on `master` (manual GitHub setting).
