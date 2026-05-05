# testrig — Specification

This document describes the public API and runtime semantics of `testrig` in its target state. It is intended for users of the framework and contributors who need to reason about behaviour without diving into the implementation.

## Purpose

`testrig` is a Go library for building and managing multi-service test environments. It orchestrates service lifecycles with dependency awareness, propagates configuration between services as they come up, and supports cross-env service reuse so the same containerised dependencies can be shared across independent `testrig.Env` instances within a process (and, with the OS-env-backed provider, across child processes spawned from the test).

The framework is designed for integration tests that need real services (databases, mocks, etc.) rather than for unit tests.

## Module

- Module path: `github.com/sha1n/testrig`
- Go version: `1.24`
- License: TBD (added separately)

## Project Layout

```
.                         — core framework (Env, Service, DiscoveryStore, SetEnvVars)
services/                 — pre-configured services (each implements testrig.Service)
  ├── postgres/           — PostgreSQL service
  └── wiremock/           — WireMock service
internal/dag/             — directed-acyclic-graph cycle validation
internal/testutil/        — shared test helpers (e.g. MockEnvContext)
examples/                 — runnable integration examples
  ├── viper-app/          — Viper config-injection example
  └── koanf-app/          — koanf config-injection example
docs/                     — specs and plans
```

> **Layout invariant:** the framework's public API lives in the module root package `testrig`. Pre-built services live under `services/<name>/` and each implements `testrig.Service`.

## Core Concepts

### `Properties`

`type Properties map[string]string`

A flat key/value map produced by services as they start. Services publish their connection details (host, port, URL, credentials, etc.) to `Properties`, where downstream services and the test code can read them.

Helpers on `Properties`:
- `Int(key) (int, error)`
- `Bool(key) (bool, error)`
- `Duration(key) (time.Duration, error)`

Missing keys return errors. The map itself is plain — callers can range over it directly.

### `Service`

```go
type Service interface {
    Name() string
    Identifier() string
    Dependencies() []string
    Start(ctx context.Context, envCtx EnvContext) (Properties, error)
    Stop(ctx context.Context) error
}
```

A `Service` is a stateful dependency with a lifecycle. The framework calls `Start` once all dependencies are running, and `Stop` once all dependents have stopped.

- `Name()` — unique within the environment; used in logs, dependency wiring, and stop-order coordination. **Per-env scope.**
- `Identifier()` — content-addressable string; identical configurations across processes produce identical identifiers, enabling cross-env and cross-process reuse via discovery. **Global scope.** It is conventional but not required to keep `Identifier` independent of `Name` so two differently-named instances of the same configuration can share a backing container.
- `Dependencies()` — names of services that must be running before this one starts. Cycles are rejected at `Env.Start()`.
- `Start` returns the properties this service contributes to the shared `Properties` map.
- `Stop` is only called for services this `Env` actually started (not for reused services).

### `EnvContext`

```go
type EnvContext interface {
    Get(key string) (string, bool)
    Int(key string) (int, error)
    Bool(key string) (bool, error)
    Duration(key string) (time.Duration, error)
    Properties() Properties
    Logger() *slog.Logger
}
```

Read-only handle into the environment, passed to `Service.Start`. Implementations are concurrency-safe; the underlying property map is locked during reads. `Logger()` returns a per-service scoped logger (`service=<name>` attribute).

### `Env`

The orchestrator. Constructed via `New(opts ...Option) (*Env, error)` (or `MustNew` for tests where invalid configuration is a programmer-checked condition). All configuration is applied at construction; the resulting `*Env` is not further mutated until `Start` is called.

```go
env, err := testrig.New(
    testrig.WithName("integration-tests"),
    testrig.WithLogger(myLogger),
    testrig.WithDiscovery(testrig.NewOsEnvDiscovery()),
    testrig.WithHooks(myHook),
    testrig.With(svc1, svc2, svc3),
)
if err != nil { /* ... */ }

if err := env.Start(ctx); err != nil { /* ... */ }
defer env.Stop(context.Background())

props := env.Properties()
```

#### Options

All options validate their input and return an error from `New` rather than panicking. `MustNew` wraps `New` and panics on error — convenient when configuration is static.

| Option | Semantics |
|---|---|
| `New()` (no options) | Defaults: name `"testenv"`, in-process `MapStore` discovery (per-`Start()` isolation), `slog.Default()` logger, no hooks, no services. |
| `WithName(string)` | Last-wins. Errors on empty. |
| `WithDiscovery(DiscoveryProvider)` | Last-wins. Errors on nil. |
| `WithLogger(*slog.Logger)` | Last-wins. Errors on nil. |
| `WithHooks(...LifecycleHook)` | **Accumulative** — appends across calls. Errors if any hook is nil. |
| `With(...Service)` | **Accumulative** — appends across calls. Errors if any service is nil. |

To build multiple envs from a shared configuration, compose options as a slice:

```go
baseOpts := []testrig.Option{testrig.With(commonSvc)}
envA := testrig.MustNew(append(baseOpts, testrig.WithName("A"))...)
envB := testrig.MustNew(append(baseOpts, testrig.WithName("B"))...)
```

#### Lifecycle methods

- `Start(ctx) error` — validates dependencies, then starts all services concurrently, each blocking on its declared dependencies. Aborts and rolls back via `Stop` on first error. Re-running on a still-running env returns an error. Re-running on an already-stopped env is allowed (state is reset on each `Start`).
- `Stop(ctx) error` — stops services in reverse-dependency order, only for services this `Env` started (reused services are left alone). Calls `Unpublish` on the discovery provider for each stopped service so dead references are not reused. Idempotent on idle envs.
- `Properties() Properties` — snapshot of current properties. Returns a copy.
- `Name() string`

#### State machine

`stateIdle → stateStarting → stateRunning → (Stop) → stateIdle`

Calling `Start` from `stateStarting` or `stateRunning` returns an error. Calling `Stop` from any state is safe.

### Discovery

Discovery enables service reuse: if a service with the same `Identifier()` was already published to the configured store and is still alive, this `Env` reuses its properties instead of starting a new container.

The scope of "already published" depends on the discovery provider:

| Provider | Scope | Use case |
|---|---|---|
| `NewDiscovery(NewMapStore())` (default) | This `Env` only — the default factory creates a fresh store per `Start()`. | Test isolation. |
| `NewDiscovery(<shared store>)` | All envs in the current process that share the store. | Multiple `Env` instances reusing the same backing services within one test binary. |
| `NewOsEnvDiscovery()` | This process and any child processes spawned afterward. | Test that spawns a subprocess (e.g. CLI under test) which needs to discover services started by the test. |

> **Not supported:** sharing services across sibling processes — e.g. between the per-package binaries that `go test ./...` runs. The OS-env-backed provider relies on env-var inheritance (parent → child), which does not propagate to siblings.

#### `DiscoveryProvider`

```go
type DiscoveryProvider interface {
    Discover(ctx, Service) (Properties, bool, error)
    Publish(ctx, Service, Properties) error
    Unpublish(ctx, Service) error
}
```

Two factories provided:
- `NewDiscovery(store DiscoveryStore) DiscoveryProvider` — backed by any `DiscoveryStore`.
- `NewOsEnvDiscovery() DiscoveryProvider` — convenience: `NewDiscovery(NewOsEnvStore())`.

Discovery key prefix: `TESTRIG_SERVICE_<Identifier>`.

#### Liveness check

After loading a published entry, discovery performs a TCP-dial liveness check using well-known property keys `<svcName>.host` and `<svcName>.port`. The dial respects the caller's context, capped at 2 s so an unbounded ctx cannot stall discovery on a slow-failing host. If those keys are not present, the check is skipped and reuse proceeds. If the dial fails (or the ctx is already expired), the entry is treated as not live and the service is started fresh.

#### `DiscoveryStore`

```go
type DiscoveryStore interface {
    Load(key string) (string, bool)
    Store(key, value string) error
    Delete(key string) error
}
```

Two implementations provided:

| Constructor | Backing | Use case |
|---|---|---|
| `NewMapStore()` | In-process map (sync-safe). | Default. Test isolation; each `Env` gets its own store on every `Start()`. No OS env mutation. |
| `NewOsEnvStore()` | OS environment variables (`os.Setenv`/`Getenv`/`Unsetenv`). | Opt-in via `NewOsEnvDiscovery()`. Visible to child processes spawned from the test. |

> Thread-safety caveat: `os.Setenv`/`Getenv` are not safe for concurrent use on all platforms. testrig serializes its own writes through a package-level mutex; this does not protect against env mutations made outside testrig.

### Lifecycle Hooks

```go
type LifecycleHook interface {
    OnStart(ctx, envCtx EnvContext) error
    OnStop(ctx, envCtx EnvContext) error
}
```

Hooks fire **after** all services in the `Env` have started (and **before** Stop is called for shutdown). They receive a stable, immutable `EnvContext` snapshot taken before `properties` is cleared, so `OnStop` sees the same view as `OnStart`. An `OnStart` failure aborts `Start` and triggers full `Stop`. `OnStop` failures are joined into the returned error so cleanup-style hooks always run.

Hooks are an opt-in convenience for cross-cutting concerns that span the whole env (e.g. running migrations after Postgres is up, writing a debug artifact, emitting env-startup metrics). Most setup is better expressed as a plain `Service` with `Dependencies` — hooks are most useful when you need OnStop to run *after* every service has stopped, which a service's own `Stop` cannot model.

### `SetEnvVars`

```go
func SetEnvVars(t *testing.T, props Properties)
```

Sets each property as an OS environment variable using `t.Setenv`, with deterministic (sorted) order. Cleanup is automatic via the `*testing.T`. Panics if the test has already called `t.Parallel()`. For parallel-safe tests, pass `env.Properties()` directly to your config library's API instead.

### Per-service logger

`Env` scopes its logger with `service=<name>` before handing it to each service via `EnvContext.Logger()`. Services that want a deeper child can compose with the standard library directly: `envCtx.Logger().With("subscope", "x")`.

## Property Injection Patterns

After `env.Start()`:

```go
props := env.Properties()

// 1. Direct map — pass to anything that takes map[string]string.
config := myLoad(props)

// 2. Viper — highest-precedence override. Parallel-safe with viper.New().
v := viper.New()
for k, val := range props { v.Set(k, val) }

// 3. koanf — confmap provider. Parallel-safe with koanf.New().
k := koanf.New(".")
k.Load(confmap.Provider(props, "."), nil)

// 4. OS env — sequential tests only (uses t.Setenv).
testrig.SetEnvVars(t, props)
```

See `examples/viper-app` and `examples/koanf-app` for full patterns.

## Pre-built services

Each package under `services/` provides a service type — a pre-configured test harness for a specific dependency. Each:

- Implements `testrig.Service`, so it can be added to an `Env` via `env.With(...)`.
- Is constructed with `New(name)` and configured via chainable `With*` methods that mutate and return `*<Type>` (no separate Builder type).
- Exposes typed-client accessors directly on the value (e.g. `*postgres.Postgres.DSN()`, `.DB(ctx)`; `*wiremock.WireMock.URL()`, `.Client()`). These are valid only after `Env.Start()`.

Configuration methods accept primitive args and do not panic on nil.

### `services/postgres`

A PostgreSQL service backed by testcontainers-go.

- Defaults: image `postgres:16-alpine`, db `testdb`, user/password `user`/`password`.
- Properties exported under fixed keys: `<name>.host`, `<name>.port`, `<name>.user`, `<name>.password`, `<name>.dbname`, `<name>.dsn`. The DSN is built via `net/url` so credentials and db names with special characters round-trip correctly.
- Identifier is a SHA-256 hash of a NUL-separated config encoding (image, tag, db, user, password) — robust against any character in any field. **Name is intentionally not part of the hash:** two Postgres instances with the same configuration but different display Names are equivalent for cross-env reuse. Use distinct configuration (different db name, image tag, etc.) to force isolation.
- `*postgres.Postgres` exposes `DSN() string` and `DB(ctx) (*sql.DB, error)`. `DB` uses the `pgx` stdlib driver and Pings the connection before returning, so failures surface at the call site rather than on first query.

### `services/wiremock`

A WireMock service backed by testcontainers-go.

- Default image `wiremock/wiremock:3.2.0`.
- Single property exported under the fixed key `<name>.url`.
- Identifier is a SHA-256 hash of (image, tag); Name is not part of the hash (same rationale as Postgres).
- `*wiremock.WireMock` exposes `URL() string` and `Client() *wiremock.Client`.

## Concurrency & Safety

- All configuration is applied at construction (`New(opts...)`); the resulting `*Env` is not mutated thereafter until `Start` is called. Calling `Start` concurrently with itself, or while the env is running, is rejected by the state machine.
- The shared `Properties` map is guarded by an internal `sync.RWMutex`; reads in `Service.Start` use the `EnvContext` accessors.
- Services are started concurrently using `errgroup`. Each service blocks on signals from its declared dependencies, ensuring `Service.Start` only sees properties of services it depends on.
- `Stop` waits for all dependents to stop before stopping a given service.

## Conventions

- Tests use the `_test` package suffix (black-box testing) by default, with white-box `internals_test.go` files where private behaviour needs coverage.
- No mocking frameworks: hand-written mocks live in test files or `internal/testutil`.
- Options validate their input and return errors from `New`; use `MustNew` if you want a panic on misconfiguration.
- Services published to discovery serialise their `Properties` as JSON.

## Build & Test

```
make check          # format + lint + test (default)
make test           # tests only
make lint           # go vet + golangci-lint
make format         # gofmt -s
make coverage       # tests with coverage report
make build-examples # build example binaries into bin/
make clean          # remove build artifacts and bin/
```

All tests are race-detector-clean: `go test ./... -race`.
