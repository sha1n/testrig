# koanf-app — testrig integration example (koanf)

Mirrors `examples/viper-app` but uses [koanf](https://github.com/knadh/koanf)
for typed configuration.

## Layout

```
main.go              — entry point: testenv.Setup → config.Load → sampleapp.New → ListenAndServe
config/              — Config struct + Load (koanf-specific)
testenv/             — testrig wiring used by both main and tests
server/              — server_test.go: integration tests; share env via TestMain
```

The HTTP server (handlers, store, routes) is in
`examples/internal/sampleapp`; the custom schema-seed `testrig.Service`
is in `examples/internal/seed` — both shared with `examples/viper-app`.

## Run

```
go run ./examples/koanf-app/
```

Requires Docker.

## Tests

- `config/config_test.go` — fast unit tests for `Load`; no env required.
- `server/server_test.go` — integration tests; shared env via `TestMain`.
