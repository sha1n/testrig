# testrig

A Go library for orchestrating multi-service test environments. Built on
[testcontainers-go](https://golang.testcontainers.org/), `testrig` adds
dependency-aware lifecycle management, property propagation between services,
and cross-env reuse so the same containerised dependencies can be shared
between independent `Env` instances.

> **Status:** pre-1.0, API not yet stable. Module path: `github.com/sha1n/testrig`.

## Why

Tests that need a real Postgres, a stubbed HTTP backend, or a chain of
collaborating services typically end up with hand-rolled `TestMain` scaffolding:
spin up containers in dependency order, plumb host/port/credentials into the
app's config, tear everything down on exit. `testrig` factors that scaffolding
into an `Env` orchestrator, leaving your tests to declare *what* they need
instead of *how* to bring it up.

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
    pg := postgres.New("pg").WithDatabase("appdb")

    env := testrig.MustNew(testrig.With(pg))
    require.NoError(t, env.Start(context.Background()))
    t.Cleanup(func() { _ = env.Stop(context.Background()) })

    // Properties published by services are addressable by "<name>.<field>".
    props := env.Properties()
    dsn := props["pg.dsn"]

    // Or use the typed accessor on the service.
    db, err := pg.DB(context.Background())
    require.NoError(t, err)
    defer db.Close()

    // ... run your test against db / dsn
}
```

## Features

- **Dependency-aware startup.** Declare `Dependencies()` on each service;
  `Env.Start` brings them up in topological order and rolls back on first error.
- **Property propagation.** Services publish a `Properties` map (host, port,
  credentials, DSNs); downstream services read it from their `EnvContext`.
- **Cross-env reuse.** Configure shared discovery and two `Env` instances with
  the same content-addressed services share a single backing container.
- **Concurrent start/stop.** Independent services start in parallel; stop runs
  in reverse-dependency order. Race-detector clean.
- **Pluggable injection.** Pass `env.Properties()` to viper, koanf, or any
  `map[string]string`-shaped config; or use `SetEnvVars(t, props)` for
  libraries that read only from `os.Getenv`.
- **Pre-built services.** `services/postgres` and `services/wiremock` ship as
  testcontainers-backed implementations. New services are a single `Service`
  interface implementation away.

## Pre-built services

| Service | Import | Notes |
|---|---|---|
| PostgreSQL | `github.com/sha1n/testrig/services/postgres` | testcontainers-backed; exposes `DSN()` and `DB(ctx)` once started. |
| WireMock | `github.com/sha1n/testrig/services/wiremock` | testcontainers-backed; exposes `URL()` and `Client()`. |

## Examples

- [`examples/viper-app`](examples/viper-app/) — config injection via Viper.
- [`examples/koanf-app`](examples/koanf-app/) — config injection via koanf.

Both demonstrate the bridging pattern between service-published keys (e.g.
`pg.dsn`) and an application's own config keys (e.g. `DATABASE_URL`).

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
