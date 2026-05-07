# viper-app — testrig integration example (Viper)

A small HTTP service wired up with [Viper](https://github.com/spf13/viper)
for config and [testrig](https://github.com/sha1n/testrig) for its test
environment.

## Layout

```
main.go              — entry point: testenv.Setup → config.Load → server.New → ListenAndServe
config/              — Config struct + Load (Viper-specific)
server/              — HTTP server: routes, handlers, DB store; integration tests share env via TestMain
testenv/             — testrig wiring used by both main and tests
seed/                — custom non-dockerized testrig.Service that applies the schema after Postgres is up
```

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
