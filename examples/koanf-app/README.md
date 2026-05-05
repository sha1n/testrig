# Koanf Configuration Example

Demonstrates integrating `testrig` with applications that use [Koanf](https://github.com/knadh/koanf) for configuration management, using parallel-safe in-memory injection.

## How it works

**Application code (`main.go`)**

- `LoadConfig(overrides map[string]string)` creates a new `koanf.Koanf` instance per call.
- Environment variables are loaded first via `env.Provider`, then overrides are layered on top via `confmap.Provider` — giving them higher precedence.
- In production, pass `nil`; koanf reads from environment as usual.

**Test code (`main_test.go`)**

- Starts a `testrig.Env` with a Postgres service.
- Bridges from the service's property keys (`pg.dsn`) to the application's config keys (`DATABASE_URL`) at the consumption site. Worth noting: koanf treats `.` as a delimiter, so passing `pg.dsn` verbatim would create a nested map rather than a flat key.
- **No `os.Setenv` calls** — properties are injected in-memory via koanf's confmap provider. Tests can safely run in parallel.

## Running

```bash
go test -v ./examples/koanf-app/...
```

(Requires Docker for the Postgres testcontainer.)
