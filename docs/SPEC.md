# testrig — Specification

This document describes the public API and runtime semantics of `testrig` in its target state. It is intended for users of the framework and contributors who need to reason about behaviour without diving into the implementation.

## Purpose

`testrig` is a Go library for building and managing multi-service test environments. It orchestrates service lifecycles in parallel and aggregates the properties they publish, so an integration test can declare *what* it needs (e.g. "a Postgres, a WireMock") and let the framework bring them up concurrently, expose their connection details to the test, and tear them down cleanly.

The framework is designed for integration tests that need real services (databases, mocks, etc.) rather than for unit tests.

## Module

- Module path: `github.com/sha1n/testrig`
- Go version: `1.24`
- License: TBD (added separately)

## Project Layout

```
.                         — core framework (Env, Service, Stages, Properties, LifecycleHook, SetEnvVars)
services/                 — pre-configured services (each implements testrig.Service)
  ├── postgres/           — PostgreSQL service
  └── wiremock/           — WireMock service
examples/                 — runnable integration examples
  ├── viper-app/          — Viper config-injection example
  └── koanf-app/          — koanf config-injection example
docs/                     — specs and plans
```

> **Layout invariant:** the framework's public API lives in the module root package `testrig`. Pre-built services live under `services/<name>/` and each implements `testrig.Service`.

## Core Concepts

### `Properties`

`type Properties map[string]string`

A flat key/value map produced by services as they start. Services publish their connection details (host, port, URL, credentials, etc.) to `Properties`. After `env.Start`, the test reads `env.Properties()` and feeds it into its config library — typed parsing belongs there, not here.

### `Service`

```go
type Service interface {
    Name() string
    Start(ctx context.Context, logger *slog.Logger) (Properties, error)
    Stop(ctx context.Context) error
}
```

A `Service` is a stateful dependency with a lifecycle. The framework calls `Start` for every service and `Stop` for every service whose `Start` returned without error. Within a stage, services start (and stop) concurrently; see `Stages` below for opt-in ordering across groups of services.

- `Name()` — unique within the environment; used in logs.
- `Start` returns the properties this service contributes to the shared `Properties` map. Sibling services within the same stage cannot observe each other's published properties; cross-service wiring belongs either in a later stage or in test setup code between `env.Start` and the assertions.
- A `Service` is owned by the `Env` it was added to; passing the same instance to multiple Envs is a programmer error.

### `Env`

The orchestrator. Constructed via `New(name string) *Env` and configured through chainable fluent methods on `*Env`. Configuration is applied before `Start`; the resulting `*Env` is not further mutated until `Start` is called.

```go
env := testrig.New("integration-tests").
    With(svc1, svc2).
    WithStages(testrig.NewStages(svc3).Then(svc4, svc5)).
    WithLogger(myLogger).
    WithLifecycleHooks(myHook)

if err := env.Start(ctx); err != nil { /* ... */ }
defer env.Stop(context.Background())

props := env.Properties()
```

#### Fluent builder methods

| Method | Semantics |
|---|---|
| `New(name string) *Env` | Creates an env with the given display name (used in logs) and `slog.Default()` logger. |
| `(*Env).With(...Service) *Env` | Appends a single-stage track containing the given services. Multiple calls accumulate as distinct tracks. Panics on nil. |
| `(*Env).WithStages(*Stages) *Env` | Appends a multi-stage track. Panics on nil. |
| `(*Env).WithLogger(*slog.Logger) *Env` | Replaces the env logger. Panics on nil. |
| `(*Env).WithLifecycleHooks(...LifecycleHook) *Env` | Appends lifecycle hooks (accumulative). Panics on nil. |

For tests that share configuration across multiple envs, factor a helper that returns `*Env` after applying common setup, then chain further methods on the returned env.

#### Lifecycle methods

- `Start(ctx) error` — runs all registered tracks concurrently; within a track, stages run sequentially. Aborts and rolls back via `Stop` on first error; rollback only stops services whose `Start` returned without error. Re-running on a still-running env returns an error. Re-running on an already-stopped env is allowed (state is reset on each `Start`).
- `Stop(ctx) error` — stops every successfully-started service. Tracks stop concurrently; within a track, stages stop in reverse order. Idempotent on idle envs and under concurrent callers.
- `Properties() Properties` — snapshot of current properties. Returns a copy. Returns an empty (non-nil) map when the env is idle (never started, or stopped).
- `Name() string`

#### State machine

`stateIdle → stateStarting → stateRunning → (Stop) → stateIdle`

Calling `Start` from `stateStarting` or `stateRunning` returns an error. Calling `Stop` from any state is safe.

### Lifecycle Hooks

```go
type LifecycleHook interface {
    AfterStart(ctx context.Context, props Properties, logger *slog.Logger) error
    AfterStop(ctx context.Context, props Properties, logger *slog.Logger) error
}
```

`AfterStart` fires once every service has started successfully and the env has transitioned to running. It is part of the `Start` sequence: returning an error aborts `Start` and triggers full rollback (`Stop` is invoked).

`AfterStop` fires once every service has stopped, as part of the `Stop` sequence. All registered hooks run even if a previous hook returned an error, so cleanup-style hooks always get a chance to execute. Returned errors are joined into the error returned by `Env.Stop`.

Hooks receive a stable property snapshot taken before `properties` is cleared, so `AfterStop` sees the same view as `AfterStart`.

Hooks are an opt-in convenience for cross-cutting concerns that span the whole env (e.g. running migrations after Postgres is up, writing a debug artifact, emitting env-startup metrics). Most setup is better expressed as a plain `Service` — hooks are most useful when you need work that runs *after* every service has stopped, which a service's own `Stop` cannot model.

### `SetEnvVars`

```go
func SetEnvVars(t *testing.T, props Properties)
```

Sets each property as an OS environment variable using `t.Setenv`, with deterministic (sorted) order. Cleanup is automatic via the `*testing.T`. Panics if the test has already called `t.Parallel()`. For parallel-safe tests, pass `env.Properties()` directly to your config library's API instead.

### `Stages`

```go
type Stages struct{ /* opaque */ }

func NewStages(services ...Service) *Stages
func (s *Stages) Then(services ...Service) *Stages
```

A *track* is a startup pipeline registered by a single `With` or `WithStages` call. Tracks run concurrently with each other; within a track, stages run sequentially.

A `Stages` describes an ordered sequence of stages for one track: within a stage services start concurrently, and stages execute one after another in declaration order. Pass to `Env.WithStages`.

Use `With` for independent services that can start in any order; reach for `WithStages` only when one service needs another to be ready before its `Start` runs (e.g. running schema migrations only after Postgres is up).

On `Stop`, stages within a track tear down in **reverse order** (last stage first); services within a stage stop concurrently. Tracks themselves stop in parallel.

### Per-service logger

`Env` scopes its logger with `service=<name>` before passing it to each `Service.Start`. Services that want a deeper child can compose with the standard library directly: `logger.With("subscope", "x")`.

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

- Implements `testrig.Service`, so it can be added to an `Env` via `(*Env).With(...)` or `(*Env).WithStages(...)`.
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

- All configuration is applied via the fluent builder before `Start`; mutating the env (or calling `Start` again) while it is running is a programmer error and is rejected by the state machine.
- Tracks are started concurrently using `errgroup`. Within a track, stages run sequentially; within a stage, services start concurrently. Each `Service.Start` receives its own scoped logger.
- The aggregated `Properties` map is guarded by an internal `sync.RWMutex`; `env.Properties()` returns a snapshot copy.
- `Stop` runs tracks concurrently and reverses stage order within each track.

## Conventions

- Tests use the `_test` package suffix (black-box testing) by default, with white-box `internals_test.go` files where private behaviour needs coverage.
- No mocking frameworks: hand-written mocks live in test files.
- Fluent builder methods panic on programmer-error inputs (nil service, nil logger, nil hook). Empty names are allowed.

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
