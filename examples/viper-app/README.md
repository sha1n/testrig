# Viper Configuration Example

Demonstrates integrating `testrig` with applications that use [Viper](https://github.com/spf13/viper) for configuration management, using parallel-safe in-memory injection.

## How it works

**Application code (`main.go`)**

- `LoadConfig(overrides map[string]string)` creates a new `viper.Viper` instance per call (not the global singleton).
- Overrides are applied via `v.Set(k, val)`, which has the **highest precedence** in Viper — above env vars, config files, and defaults.
- In production, pass `nil`; Viper reads from environment as usual.

**Test code (`main_test.go`)**

- Starts a `testrig.Env` with a Postgres service.
- Bridges from the service's property keys (`pg.dsn`) to the application's config keys (`DATABASE_URL`) at the consumption site.
- Passes the result as overrides to `LoadConfig`.
- **No `os.Setenv` calls** — properties are injected in-memory via Viper's API. Tests can safely run in parallel.

## Running

```bash
go test -v ./examples/viper-app/...
```

(Requires Docker for the Postgres testcontainer.)
