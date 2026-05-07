# koanf-app — testrig integration example (koanf)

Mirrors `examples/viper-app` but uses [koanf](https://github.com/knadh/koanf)
for typed configuration.

## Layout

```
main.go              — entry point: testenv.Setup → config.Load → server.New → ListenAndServe
config/              — Config struct + Load (koanf-specific)
server/              — HTTP server: routes, handlers, DB store; integration tests share env via TestMain
testenv/             — testrig wiring used by both main and tests
seed/                — custom non-dockerized testrig.Service that applies the schema after Postgres is up
```

## Run

```
go run ./examples/koanf-app/
```

Requires Docker.

## Tests

- `config/config_test.go` — fast unit tests for `Load`; no env required.
- `server/server_test.go` — integration tests; shared env via `TestMain`.
