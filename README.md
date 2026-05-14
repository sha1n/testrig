# testrig

A Go library for orchestrating multi-service test environments. Built on
[testcontainers-go](https://golang.testcontainers.org/), `testrig` adds
parallel lifecycle management and property aggregation across
services so an integration test can declare *what* it needs and let the
framework handle bringing it up, wiring it into the application's config,
and tearing it down.

> **Status:** pre-1.0, API not yet stable. Module path: `github.com/sha1n/testrig`.

## Install

The engine and each pre-built service are independently versioned Go modules.
Add only what you need:

```sh
# Engine — required. Stdlib + golang.org/x/sync only.
go get github.com/sha1n/testrig@v0.0.0-prototype.1

# Pre-built services (each pulls its own deps; pick what you use):
go get github.com/sha1n/testrig/services/postgres@v0.0.0-prototype.1
go get github.com/sha1n/testrig/services/wiremock@v0.0.0-prototype.1
go get github.com/sha1n/testrig/services/oidc@v0.0.0-prototype.1
```

While the API is still iterating, the only published tags are
`v0.0.0-prototype.*` prereleases. **Pin explicitly** rather than using
`@latest`: today `@latest` resolves to the highest prerelease, but the
moment a non-prerelease tag (e.g. `v0.1.0`) is published, `@latest`
will silently switch to it and skip all prereleases — possibly across
breaking changes while pre-1.0.

The `examples/` and `tools/` modules are repository-internal (demo apps and
the pinned `golangci-lint` for `make lint` respectively); they are not
intended to be imported by external projects and have no published tags.

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
    props, err := env.Start(context.Background())
    require.NoError(t, err)
    t.Cleanup(func() { _ = env.Stop(context.Background()) })

    // Properties are ready to merge into your app's config loader.
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
- **Pre-built services.** `services/postgres`, `services/wiremock`, and
  `services/oidc` ship as ready-to-use implementations (the first two
  testcontainers-backed; OIDC is a non-dockerized in-process issuer).
  New services are a single `Service` interface implementation away
  (3 methods: `Name`, `Start`, `Stop`).

## Pre-built services

Each pre-built service is a separate Go module. `go get github.com/sha1n/testrig`
pulls only the engine; testcontainers, pgx, JWT, and other heavy deps come in
only when you import a service.

Each service has its own README with a quickstart, full configuration
reference, and a "Gaps and workarounds" section.

| Service | Import | Notes |
|---|---|---|
| [PostgreSQL](services/postgres/README.md) | `github.com/sha1n/testrig/services/postgres` | testcontainers-backed; exposes `DSN()` and `DB(ctx)` once started; all property keys customizable. |
| [WireMock](services/wiremock/README.md) | `github.com/sha1n/testrig/services/wiremock` | testcontainers-backed; exposes `URL()` and `Client()`; URL property key customizable. |
| [OIDC](services/oidc/README.md) | `github.com/sha1n/testrig/services/oidc` | non-dockerized, Auth0-style OIDC issuer; supports `authorization_code` (with PKCE S256), `client_credentials`, and `refresh_token` grants; serves discovery, JWKS, `/authorize`, `/token`, `/userinfo`. |

## Examples

Two parallel example apps demonstrating testrig with different config
libraries. Each is a small, well-structured Go server with:

- `main.go` — thin entry point
- `config/` — typed config loader (Viper or koanf — the only divergent piece)
- `testenv/` — testrig wiring used by both `main` and the tests
- `server/server_test.go` — integration tests sharing one env via `TestMain`

The HTTP server itself and the custom schema-seed `testrig.Service` are
shared between the two examples in `examples/internal/sampleapp` and
`examples/internal/seed` — they have no config-library opinion. The seed
package is the canonical demo of how to write your own
**non-dockerized `testrig.Service`** and order it after a dependency via
`WithStages`.

| Example | Config library |
|---|---|
| [`examples/viper-app`](examples/viper-app/) | [Viper](https://github.com/spf13/viper) |
| [`examples/koanf-app`](examples/koanf-app/) | [koanf](https://github.com/knadh/koanf) |

## Spec

The full public-API and runtime-semantics specification lives at
[`docs/SPEC.md`](docs/SPEC.md).

## Repository layout

This repo is a Go multi-module workspace. Each module is independently
versioned and published, so consumers download only the dependencies they
actually use.

```
.                              github.com/sha1n/testrig            (engine; stdlib + golang.org/x/sync)
services/oidc/                 github.com/sha1n/testrig/services/oidc
services/postgres/             github.com/sha1n/testrig/services/postgres
services/wiremock/             github.com/sha1n/testrig/services/wiremock
examples/                      github.com/sha1n/testrig/examples   (not published)
tools/                         github.com/sha1n/testrig/tools      (not published; pins golangci-lint)
go.work                        ties all of the above together for local development
```

Each sub-module imports the engine via `github.com/sha1n/testrig`, and
each sub-module `go.mod` carries a `require github.com/sha1n/testrig
<engine version>` plus a `replace github.com/sha1n/testrig => ../..`.
The replace lets local builds work before any engine version is
published; it has no effect on external consumers (Go ignores `replace`
directives in dependency modules). `go.work` ties all modules together
for editor and tool support.

### Tagging and releases

Each module is versioned independently using the standard Go sub-module
tag scheme:

| Module | Tag prefix | Example |
|---|---|---|
| root | (none) | `v0.1.0` |
| `services/oidc` | `services/oidc/` | `services/oidc/v0.1.0` |
| `services/postgres` | `services/postgres/` | `services/postgres/v0.1.0` |
| `services/wiremock` | `services/wiremock/` | `services/wiremock/v0.1.0` |

Release order: tag the engine first, then bump each sub-module's
`require github.com/sha1n/testrig` to the engine's new version, commit,
then tag the sub-modules. The `replace` line stays in place — it's
inert for consumers and keeps local development working.

## Build

```
make check           # format + lint + test across every module (default)
make test            # tests only (every module)
make lint            # go vet + golangci-lint across every module; fails on issues
make build-examples  # build example binaries into bin/
make go-get          # workspace sync across every module
```

`make lint` invokes `golangci-lint` via Go's `tool` directive — the version is
pinned in `tools/go.mod`, so contributors don't need to install it separately
and CI runs the same version.

Requires Go 1.25 or later. Tests require Docker (testcontainers).
