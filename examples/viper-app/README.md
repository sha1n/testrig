# viper-app — testrig integration example (Viper)

A small HTTP service wired up with [Viper](https://github.com/spf13/viper)
for config and [testrig](https://github.com/sha1n/testrig) for its test
environment.

## Layout

```
main.go              — entry point: testenv.Setup → config.Load → sampleapp.New → ListenAndServe
config/              — Config struct + Load (Viper-specific)
testenv/             — testrig wiring used by both main and tests
server/              — server_test.go: integration tests; share env via TestMain
```

The HTTP server itself (handlers, store, routes) lives in
`examples/internal/sampleapp` — shared with `examples/koanf-app` since
that code has no config-library opinion. The custom schema-seed
`api.Service` lives in `examples/internal/seed` for the same reason.

## Run

```
go run ./examples/viper-app/
```

Requires Docker (testcontainers spins up Postgres + WireMock). The remote
mock has no stubs in the standalone run, so /save will record an
"unmatched" body. The integration tests stub it explicitly.

## Tests

- `config/config_test.go` — fast unit tests for `Load`; no env required.
- `server/server_test.go` — integration tests; one shared env via
  `TestMain` (one Postgres, one WireMock, one schema seed for the whole
  suite). Each test resets WireMock state and uses a unique DB key.
