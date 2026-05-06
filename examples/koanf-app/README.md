# Koanf Configuration Example

A runnable end-to-end demo of integrating `testrig` with a [koanf](https://github.com/knadh/koanf)-based application that depends on **two** services: a database and a remote HTTP service.

> **Intent (do not drift):** this example is a real, working integration.
> `main.go` uses `testrig.Env` to bring up Postgres + WireMock containers,
> feeds `env.Properties()` into koanf, runs a schema migration, and starts an
> HTTP server that does meaningful work (calls the remote, persists the
> response). The test calls the same `setupEnv` function, stubs the remote,
> hits the app's API, and polls the DB for the expected state.

## What's in here

| File | Role |
|---|---|
| `main.go` | The "test app" — koanf-based HTTP service plus the testrig integration. `setupEnv` brings up Postgres + WireMock, builds the `Config`, migrates the schema, and returns a `*Setup`. `main` is a runnable demo that boots the env and serves on the configured port. |
| `main_test.go` | End-to-end test: stubs WireMock, calls `POST /save`, polls the DB until the expected row appears. Plus pure validation tests for `loadConfig`. |

## What the app does

```
       POST /save?key=K              GET /lookup?key=K
client ───────────────► app ────────────────────────► remote (WireMock)
                        │             body
                        │              ▼
                        ▼          Postgres (responses table)
                  202 Accepted
```

- `POST /save?key=K` — fires an async lookup against `REMOTE_URL/lookup?key=K`,
  stores the response body in the `responses` table under `K`, returns 202.
- `GET /lookup?key=K` — returns the stored response body, or 404 if not found.

The async write is what makes the test's polling pattern meaningful: the test
hits `/save`, gets 202 back, and then polls the DB until the row shows up.

## How the integration works

The Postgres service supports `WithDSNPropertyName(string)` and the WireMock
service supports `WithURLPropertyName(string)`, so each service publishes its
output directly under the application's expected config keys:

```go
pg := postgres.New("pg").
    WithDatabase("koanf_app").
    WithDSNPropertyName("DATABASE_URL")
wm := wiremock.New("wm").
    WithURLPropertyName("REMOTE_URL")

env := testrig.MustNew(testrig.With(pg, wm))
env.Start(ctx)

overrides := env.Properties() // already contains DATABASE_URL and REMOTE_URL
overrides["APP_PORT"] = strconv.Itoa(freePort()) // OS-assigned, never static

cfg, _ := loadConfig(overrides) // koanf confmap.Provider on top of env vars
```

`APP_PORT` is resolved via `freePort()` (a `net.Listen("tcp", "127.0.0.1:0")` /
`Close` round-trip) so the value is never hardcoded — tests are flake-free
even when run in parallel, and the demo binds to whatever the OS hands out.

`env.Properties()` returns a flat `map[string]string` ready to merge with any
application-only keys and hand to a koanf-driven `loadConfig`. koanf's
`confmap.Provider` is layered on top of the env-var provider, so testrig
values override anything from the environment.

> Why publishing under flat keys matters extra here: koanf treats `.` as a
> delimiter, so the service's default keys like `pg.dsn` would create a
> nested map rather than a flat config key. Publishing under flat keys via
> `WithDSNPropertyName("DATABASE_URL")` and `WithURLPropertyName("REMOTE_URL")`
> sidesteps that.

The integration is parallel-safe: properties are injected via in-memory koanf
rather than `os.Setenv`, so each test owns its own `koanf.Koanf`.

## How the test asserts async state

```go
require.Eventually(t, func() bool {
    var got string
    err := s.DB.QueryRow(`SELECT response FROM responses WHERE key = $1`, "alpha").Scan(&got)
    return err == nil && got == expectedBody
}, 5*time.Second, 50*time.Millisecond, "DB row for key=alpha did not contain expected body within timeout")
```

`require.Eventually` polls the DB at a short interval until the predicate
returns true or the timeout fires — the canonical way to wait on async work
in an integration test.

## Run the demo

```bash
go run ./examples/koanf-app/
# logs:  testrig env up; serving on :<port> (DATABASE_URL=..., REMOTE_URL=...)
# pick the port from the log line and use it:
curl -X POST 'http://localhost:<port>/save?key=foo'
# → 202 Accepted; the app calls the (unstubbed) WireMock and stores the body
curl 'http://localhost:<port>/lookup?key=foo'
```

## Run the tests

```bash
go test -v ./examples/koanf-app/...
```

Requires Docker for the Postgres + WireMock testcontainers.
