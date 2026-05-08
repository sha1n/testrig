# postgres — testrig service

A testcontainers-backed PostgreSQL service for integration tests.

`services/postgres` wraps the [testcontainers-go postgres module](https://golang.testcontainers.org/modules/postgres/) and implements `testrig.Service` so it integrates directly with `testrig.Env`. On `Start` it launches a Postgres container, waits for it to accept connections, and publishes host, port, credentials, and a fully-constructed DSN as `testrig.Properties`. Once started, typed accessors (`DSN()`, `DB(ctx)`) give tests a direct handle to the running instance.

## Install

This is a separate Go module. Add it explicitly when you need it:

```
go get github.com/sha1n/testrig/services/postgres
```

It transitively pulls in `github.com/sha1n/testrig` and the testcontainers / pgx stack.

## Quickstart

```go
import (
    "context"
    "testing"

    "github.com/sha1n/testrig"
    "github.com/sha1n/testrig/services/postgres"
)

func TestMyFeature(t *testing.T) {
    // Publish the DSN directly under the application's expected config key.
    pg := postgres.New("pg").
        WithDatabase("myapp").
        WithDSNPropertyName("DATABASE_URL")

    env := testrig.New("test").With(pg)
    if err := env.Start(context.Background()); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = env.Stop(context.Background()) })

    // env.Properties() contains "DATABASE_URL" — pass it straight to your
    // config loader (viper, koanf, etc.).
    dsn := env.Properties()["DATABASE_URL"]
    _ = dsn

    // Or get a pinged *sql.DB directly from the service handle.
    db, err := pg.DB(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    defer db.Close()

    // run queries against db
}
```

## Configuration

All `With*` setters are chainable and must be called before `env.Start`.

### Container image

| Setter | Default | Notes |
|---|---|---|
| `WithImage(image string)` | `"postgres"` | Docker image name. |
| `WithTag(tag string)` | `"16-alpine"` | Docker image tag. To pin a different major version (e.g. Postgres 15), set this. |

### Database credentials

| Setter | Default | Notes |
|---|---|---|
| `WithDatabase(name string)` | `"testdb"` | Name of the database created on container startup. |
| `WithUsername(user string)` | `"user"` | Login role created on container startup. |
| `WithPassword(pass string)` | `"password"` | Password for the login role. Special characters are safe — the DSN is assembled with `net/url` so credentials are percent-encoded. |

### Property key overrides

By default the service publishes properties under `<name>.<field>` keys. Use these setters to publish directly under the keys your application's config loader expects, eliminating any bridging step in test setup code.

| Setter | Default key | What it controls |
|---|---|---|
| `WithHostPropertyName(name string)` | `"<name>.host"` | Container host (`localhost` in most Docker environments). |
| `WithPortPropertyName(name string)` | `"<name>.port"` | Mapped host port (ephemeral; assigned by Docker). |
| `WithUsernamePropertyName(name string)` | `"<name>.user"` | Database username. |
| `WithPasswordPropertyName(name string)` | `"<name>.password"` | Database password. |
| `WithDatabasePropertyName(name string)` | `"<name>.dbname"` | Database name. |
| `WithDSNPropertyName(name string)` | `"<name>.dsn"` | Fully-constructed DSN (e.g. `"DATABASE_URL"`). |

When a property key is overridden, **only** the overridden key is published — the default key is not present in the map. Each key is independently overridable; you can rename some and leave others at their defaults.

## Published properties

After `env.Start` returns, `env.Properties()` contains these entries (using default key names for a service created as `postgres.New("pg")`):

| Key | Example value | Notes |
|---|---|---|
| `pg.host` | `"localhost"` | Reachable host for the mapped port. |
| `pg.port` | `"54321"` | Ephemeral host port mapped to container port 5432. |
| `pg.user` | `"user"` | Database username. |
| `pg.password` | `"password"` | Database password. |
| `pg.dbname` | `"testdb"` | Database name. |
| `pg.dsn` | `"postgres://user:password@localhost:54321/testdb?sslmode=disable"` | Ready-to-use connection string; credentials are percent-encoded when necessary. |

The DSN always has `sslmode=disable` appended. There is no `With*` setter to change this.

## Typed accessors

These methods are only valid after `Start` has returned without error. Calling them on a service that has not been started (or has been stopped) will produce an empty or invalid DSN and a connection error.

### `DSN() string`

Returns the same DSN string that was published to `env.Properties()`. Useful when you hold a reference to the `*postgres.Postgres` value and prefer not to look up the property by key.

```go
dsn := pg.DSN() // identical to env.Properties()["pg.dsn"]
```

### `DB(ctx context.Context) (*sql.DB, error)`

Opens a `*sql.DB` using the `pgx` driver and immediately pings the server. Returns a verified, ready-to-use connection pool, or an error if the open or ping fails. The caller is responsible for closing the returned `*sql.DB`.

```go
db, err := pg.DB(context.Background())
if err != nil {
    t.Fatal(err)
}
defer db.Close()
```

Unlike a bare `sql.Open`, `DB()` surfaces connection errors immediately rather than deferring them to the first query.

Each call to `DB()` opens a new `*sql.DB` (a new connection pool). If you need a single shared pool across multiple test helpers, call `DB()` once and pass the result around.

## Lifecycle

**Start** runs `postgres.Run(...)` from the testcontainers-go postgres module. The service waits for two conditions before returning:

- The log line `"database system is ready to accept connections"` appears twice (the first occurs during WAL recovery, the second after the server is fully accepting client connections). Timeout: 30 seconds.
- Port 5432/tcp inside the container is listening. Timeout: 30 seconds.

Neither timeout is configurable via a `With*` setter. If your environment is slow to pull images, the container must be ready within 30 seconds of the port becoming available.

**Stop** calls `Terminate` on the container and clears all runtime state. Safe to call before `Start` or more than once — a no-op if the container is not running.

**Restart** is supported: calling `Start` after `Stop` launches a fresh container with a clean database. The ephemeral port will change between restarts.

**Concurrent `Start`** is an error: calling `Start` a second time without an intervening `Stop` returns `"postgres service <name> already started"`.

**`testrig.Env` ownership:** pass the service to one `Env` only. Sharing a `*postgres.Postgres` instance across multiple `Env` values is a programmer error (the `Env` contract requires exclusive ownership).

## Gaps and workarounds

**Default image is Postgres 16-alpine.** The tag `"16-alpine"` is hardcoded as the default. To test against a different version, call `WithTag("15-alpine")` or `WithTag("17")`. The image pull happens at `Start` time; CI environments without a pre-pulled image will download it on the first run.

**Container startup time.** Cold starts (no local image cache) include a Docker image pull. Warm starts (image cached) still wait for the Postgres readiness conditions, which typically adds a few seconds per test run. There is no way to share a single container across multiple test binaries; each `testrig.Env` gets its own container lifecycle.

**Startup timeout is fixed at 30 seconds.** Neither the log-wait nor the port-wait timeout is exposed via a `With*` setter. Slow CI machines that take longer than 30 seconds after the port is bound will see a timeout error.

**`sslmode=disable` is always set.** The DSN always includes `?sslmode=disable`. There is no option to enable TLS; the container does not have TLS configured and the parameter cannot be overridden.

**Connection pool is not configurable.** `DB()` returns a `*sql.DB` with Go's default pool settings (`MaxOpenConns` unlimited, `MaxIdleConns` 2). If your test needs specific pool sizing, configure it on the returned handle:

```go
db, err := pg.DB(context.Background())
if err != nil {
    t.Fatal(err)
}
db.SetMaxOpenConns(5)
db.SetMaxIdleConns(5)
```

**No schema migration support.** The service creates the database but applies no DDL. The canonical pattern is to write a thin `testrig.Service` that calls `pg.DB(ctx)`, executes your DDL, and is ordered after the postgres service via `WithStages`:

```go
// In your testenv package:
env := testrig.New("app").
    WithStages(testrig.NewStages(pg).Then(seedSvc)).
    With(otherService)
```

See `examples/internal/seed` for a complete, reusable example of this pattern.

**Single database per container.** The container is initialized with one database, one user, and one password. There is no support for creating multiple databases or roles within a single container. If your test needs multiple databases, create multiple `*postgres.Postgres` services.

**No custom `postgresql.conf` or initialization scripts.** The testcontainers-go postgres module supports init SQL scripts and config overrides, but this wrapper does not expose those options. If you need them, implement a custom `testrig.Service` that calls `postgres.Run(...)` directly with the additional options.
