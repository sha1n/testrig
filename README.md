# testrig

A Go library for orchestrating multi-service test environments. Built on
[testcontainers-go](https://golang.testcontainers.org/), `testrig` adds
parallel lifecycle management and property aggregation across
services so an integration test can declare *what* it needs and let the
framework handle bringing it up, wiring it into the application's config,
and tearing it down.

> **Status:** pre-1.0, API not yet stable. Module path: `github.com/sha1n/testrig`.

## Why

Tests that need a real Postgres, a stubbed HTTP backend, or a handful of
collaborating services typically end up with hand-rolled `TestMain` scaffolding:
spin up containers, aggregate host/port/credentials into the app's config,
tear everything down on exit. `testrig` factors that scaffolding into an
`Env` orchestrator that brings services up in parallel, gathers their published
properties, and tears them down on rollback or test exit.

## Quickstart

```go
import (
    "context"
    "testing"

    "github.com/sha1n/testrig"
    "github.com/sha1n/testrig/services/postgres"
    "github.com/stretchr/testify/require"
)

func TestSomething(t *testing.T) {
    // Publish the DSN directly under the application's expected config key.
    pg := postgres.New("pg").
        WithDatabase("appdb").
        WithDSNPropertyName("DATABASE_URL")

    env := testrig.New("test").With(pg)
    require.NoError(t, env.Start(context.Background()))
    t.Cleanup(func() { _ = env.Stop(context.Background()) })

    // Properties are ready to merge into your app's config loader.
    props := env.Properties()
    dsn := props["DATABASE_URL"]
    _ = dsn

    // Or use the typed accessor on the service.
    db, err := pg.DB(context.Background())
    require.NoError(t, err)
    defer db.Close()

    // ... run your test against db / dsn
}
```

## Features

- **Parallel start, parallel stop.** All services start concurrently;
  rollback on failure stops only those whose Start succeeded.
- **Property aggregation.** Services publish a `Properties` map (host, port,
  credentials, DSNs); `env.Properties()` returns a stable snapshot for the
  test to read.
- **App-aligned property keys.** Each pre-built service supports
  `WithXxxPropertyName(...)` so its outputs land directly under the
  application's expected config keys — no bridging step in the test.
- **Pluggable injection.** Pass `env.Properties()` to viper, koanf, or any
  `map[string]string`-shaped config; or use `SetEnvVars(t, props)` for
  libraries that read only from `os.Getenv`.
- **Opt-in startup ordering.** Use `testrig.NewStages(a).Then(b, c)` and
  `env.WithStages(...)` when you need explicit ordering between groups
  of services.
- **Pre-built services.** `services/postgres` and `services/wiremock` ship as
  testcontainers-backed implementations. New services are a single `Service`
  interface implementation away (3 methods: `Name`, `Start`, `Stop`).

## Pre-built services

| Service | Import | Notes |
|---|---|---|
| PostgreSQL | `github.com/sha1n/testrig/services/postgres` | testcontainers-backed; exposes `DSN()` and `DB(ctx)` once started; all property keys customizable. |
| WireMock | `github.com/sha1n/testrig/services/wiremock` | testcontainers-backed; exposes `URL()` and `Client()`; URL property key customizable. |

## Examples

Two parallel example apps demonstrating testrig with different config
libraries. Each is a small, well-structured Go server with:

- `main.go` — thin entry point
- `config/` — typed config loader
- `server/` — HTTP server (routes, handlers, DB store) with integration
  tests that share one test environment via `TestMain`
- `testenv/` — testrig wiring used by both `main` and the tests
- `seed/` — a **custom non-dockerized testrig.Service** that applies
  schema DDL after Postgres is up; demonstrates how to write your own
  `testrig.Service` and order it after a dependency via `WithStages`

| Example | Config library |
|---|---|
| [`examples/viper-app`](examples/viper-app/) | [Viper](https://github.com/spf13/viper) |
| [`examples/koanf-app`](examples/koanf-app/) | [koanf](https://github.com/knadh/koanf) |

## Spec

The full public-API and runtime-semantics specification lives at
[`docs/SPEC.md`](docs/SPEC.md).

## Build

```
make check           # format + lint + test (default)
make test            # tests only
make build-examples  # build example binaries into bin/
```

Requires Go 1.24 or later. Tests require Docker (testcontainers).
