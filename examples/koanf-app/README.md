# Koanf Configuration Example

A runnable end-to-end demo of integrating `testrig` with a [koanf](https://github.com/knadh/koanf)-based application.

> **Intent (do not drift):** this example is a real, working demo. `main.go`
> uses `testrig.Env` to bring up a Postgres container, feeds `env.Properties()`
> into koanf, and starts an HTTP server configured from the result. The test
> calls the same `setupEnv` function. Both the demo and the test exercise the
> same integration code.

## What's in here

| File | Role |
|---|---|
| `main.go` | The "test app" — koanf-based HTTP service. Also the testrig integration: `setupEnv` brings up Postgres and produces a `*Config`. `main` is a runnable demo that boots the env and serves `/health`. |
| `main_test.go` | Tests that exercise `setupEnv` and the resulting handler against a real container. |

## How the integration works

The Postgres service supports `WithDSNPropertyName(string)` so its DSN is
published directly under whatever config key the application uses. The koanf
example uses `DATABASE_URL`:

```go
env := testrig.MustNew(testrig.With(
    postgres.New("pg").
        WithDatabase("koanf_db").
        WithDSNPropertyName("DATABASE_URL"),
))
env.Start(ctx)

overrides := env.Properties()  // already contains DATABASE_URL
overrides["APP_PORT"] = "9090" // app-only key

cfg, _ := loadConfig(overrides) // koanf confmap.Provider on top of env vars
```

`env.Properties()` returns a flat `map[string]string` ready to merge with any
application-only keys and hand to a koanf-driven `loadConfig`. koanf's
`confmap.Provider` is layered on top of the env-var provider, so testrig
values override anything from the environment.

> Why publishing under a flat key matters extra here: koanf treats `.` as a
> delimiter, so the service's default keys like `pg.dsn` would create a
> nested map rather than a flat config key. `WithDSNPropertyName("DATABASE_URL")`
> sidesteps that.

The integration is parallel-safe: properties are injected via in-memory koanf
rather than `os.Setenv`, so each test owns its own `koanf.Koanf`.

## Run the demo

```bash
go run ./examples/koanf-app/
# starts a Postgres container, configures the app, serves :9090
curl http://localhost:9090/health
# → OK - postgres://user:password@localhost:<port>/koanf_db?sslmode=disable
```

## Run the tests

```bash
go test -v ./examples/koanf-app/...
```

Requires Docker for the Postgres testcontainer.
