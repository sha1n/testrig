# Viper Configuration Example

A runnable end-to-end demo of integrating `testrig` with a [Viper](https://github.com/spf13/viper)-based application.

> **Intent (do not drift):** this example is a real, working demo. `main.go`
> uses `testrig.Env` to bring up a Postgres container, feeds `env.Properties()`
> into Viper, and starts an HTTP server configured from the result. The test
> calls the same `setupEnv` function. Both the demo and the test exercise the
> same integration code.

## What's in here

| File | Role |
|---|---|
| `main.go` | The "test app" — Viper-based HTTP service. Also the testrig integration: `setupEnv` brings up Postgres and produces a `*Config`. `main` is a runnable demo that boots the env and serves `/health`. |
| `main_test.go` | Tests that exercise `setupEnv` and the resulting handler against a real container. |

## How the integration works

The Postgres service supports `WithDSNPropertyName(string)` so its DSN is
published directly under whatever config key the application uses. The viper
example uses `DATABASE_URL`:

```go
env := testrig.MustNew(testrig.With(
    postgres.New("pg").
        WithDatabase("viper_db").
        WithDSNPropertyName("DATABASE_URL"),
))
env.Start(ctx)

overrides := env.Properties()  // already contains DATABASE_URL
overrides["APP_PORT"] = "8080" // app-only key

cfg, _ := loadConfig(overrides) // Viper.Set: highest-precedence layer
```

`env.Properties()` returns a flat `map[string]string` ready to merge with any
application-only keys and hand to a Viper-driven `loadConfig`. Viper's
`Set()` is the highest-precedence layer (above env vars, files, defaults), so
the testrig values win.

The integration is parallel-safe: properties are injected via in-memory
`viper.Set` rather than `os.Setenv`, so each test owns its own
`viper.Viper`.

## Run the demo

```bash
go run ./examples/viper-app/
# starts a Postgres container, configures the app, serves :8080
curl http://localhost:8080/health
# → OK - postgres://user:password@localhost:<port>/viper_db?sslmode=disable
```

## Run the tests

```bash
go test -v ./examples/viper-app/...
```

Requires Docker for the Postgres testcontainer.
