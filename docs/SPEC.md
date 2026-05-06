# testrig — Specification

This document describes the public API and runtime semantics of `testrig` in its target state. It is intended for users of the framework and contributors who need to reason about behaviour without diving into the implementation.

## Purpose

`testrig` is a Go library for building and managing multi-service test environments. It orchestrates service lifecycles with dependency awareness and propagates configuration between services as they come up, so an integration test can declare *what* it needs (e.g. "a Postgres, a WireMock") and let the framework bring them up in the right order, expose their connection details to the test, and tear them down cleanly.

The framework is designed for integration tests that need real services (databases, mocks, etc.) rather than for unit tests.

## Module

- Module path: `github.com/sha1n/testrig`
- Go version: `1.24`
- License: TBD (added separately)

## Project Layout

```
.                         — core framework (Env, Service, SetEnvVars)
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
    Dependencies() []string
    Start(ctx context.Context, envCtx EnvContext) (Properties, error)
    Stop(ctx context.Context) error
}
```

A `Service` is a stateful dependency with a lifecycle. The framework calls `Start` once all dependencies are running, and `Stop` once all dependents have stopped.

- `Name()` — unique within the environment; used in logs, dependency wiring, and stop-order coordination.
- `Dependencies()` — names of services that must be running before this one starts. Cycles are rejected at `Env.Start()`.
- `Start` returns the properties this service contributes to the shared `Properties` map.
- `Stop` is invoked in reverse-dependency order. A `Service` is owned by the `Env` it was added to; passing the same instance to multiple Envs is a programmer error.

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
| `New()` (no options) | Defaults: name `"testenv"`, `slog.Default()` logger, no hooks, no services. |
| `WithName(string)` | Last-wins. Errors on empty. |
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
- `Stop(ctx) error` — stops services in reverse-dependency order. Idempotent on idle envs and under concurrent callers.
- `Properties() Properties` — snapshot of current properties. Returns a copy. Returns an empty (non-nil) map when the env is idle (never started, or stopped).
- `Name() string`

#### State machine

`stateIdle → stateStarting → stateRunning → (Stop) → stateIdle`

Calling `Start` from `stateStarting` or `stateRunning` returns an error. Calling `Stop` from any state is safe.

### Lifecycle Hooks

```go
type LifecycleHook interface {
    AfterStart(ctx, envCtx EnvContext) error
    AfterStop(ctx, envCtx EnvContext) error
}
```

`AfterStart` fires once every service has started successfully and the env has transitioned to running. It is part of the `Start` sequence: returning an error aborts `Start` and triggers full rollback (`Stop` is invoked).

`AfterStop` fires once every service has stopped, as part of the `Stop` sequence. All registered hooks run even if a previous hook returned an error, so cleanup-style hooks always get a chance to execute. Returned errors are joined into the error returned by `Env.Stop`.

Hooks receive a stable, immutable `EnvContext` snapshot taken before `properties` is cleared, so `AfterStop` sees the same view as `AfterStart`.

Hooks are an opt-in convenience for cross-cutting concerns that span the whole env (e.g. running migrations after Postgres is up, writing a debug artifact, emitting env-startup metrics). Most setup is better expressed as a plain `Service` with `Dependencies` — hooks are most useful when you need work that runs *after* every service has stopped, which a service's own `Stop` cannot model.

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
- Is constructed with `New(name)` and configured via chainable `With*` methods that mutate and return the value (no separate Builder type).
- Exposes typed-client accessors directly (e.g. `*postgres.Postgres.DSN()`, `.DB(ctx)`; `*wiremock.WireMock.URL()`, `.Client()`). These are valid only after `Env.Start()`.
- Publishes its outputs as `Properties` under `<name>.<field>` keys by default; each key can be overridden via a `WithXxxPropertyName` setter so the service publishes directly under the application's expected config keys.

Configuration methods accept primitive args and do not panic on nil.

### `services/postgres`

A PostgreSQL service backed by testcontainers-go.

- Defaults: image `postgres:16-alpine`, db `testdb`, user/password `user`/`password`.
- Default property keys: `<name>.host`, `<name>.port`, `<name>.user`, `<name>.password`, `<name>.dbname`, `<name>.dsn`. Each is independently overridable via `WithHostPropertyName`, `WithPortPropertyName`, `WithUsernamePropertyName`, `WithPasswordPropertyName`, `WithDatabasePropertyName`, `WithDSNPropertyName` — typically used to publish the DSN under the application's expected key (e.g. `DATABASE_URL`) so no bridging is needed at the test level.
- The DSN is built via `net/url` so credentials and db names with special characters round-trip correctly.
- `*postgres.Postgres` exposes `DSN() string` and `DB(ctx) (*sql.DB, error)`. `DB` uses the `pgx` stdlib driver and Pings the connection before returning, so failures surface at the call site rather than on first query.

### `services/wiremock`

A WireMock service backed by testcontainers-go.

- Default image `wiremock/wiremock:3.2.0`.
- Default property key: `<name>.url`. Override via `WithURLPropertyName`.
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
