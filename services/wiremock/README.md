# wiremock — testrig service

Containerized WireMock stub server, wired into a `testrig.Env`.

`services/wiremock` wraps the official `wiremock/wiremock` Docker image via
[testcontainers-go](https://golang.testcontainers.org/) and exposes a
[`go-wiremock`](https://github.com/wiremock/go-wiremock) typed client so tests
can stub HTTP responses with a fluent Go API. The service publishes its base URL
as a `testrig.Properties` entry so it lands directly in the application's config
loader without any bridging code.

## Quickstart

```go
package myservice_test

import (
    "context"
    "net/http"
    "testing"

    "github.com/sha1n/testrig"
    "github.com/sha1n/testrig/services/wiremock"
    "github.com/stretchr/testify/require"
    gowiremock "github.com/wiremock/go-wiremock"
)

func TestMyService(t *testing.T) {
    // Construct and configure the service.
    wm := wiremock.New("wm").
        WithURLPropertyName("REMOTE_URL") // match your app's config key

    // Add it to an env and start.
    env := testrig.New("test").With(wm)
    require.NoError(t, env.Start(context.Background()))
    t.Cleanup(func() { _ = env.Stop(context.Background()) })

    // The URL is available via env.Properties() or the typed accessor.
    _ = env.Properties()["REMOTE_URL"] // e.g. "http://localhost:49175"
    _ = wm.URL()                        // same value

    // Stub a response.
    require.NoError(t, wm.Client().StubFor(
        gowiremock.Get(gowiremock.URLPathEqualTo("/ping")).
            WillReturnResponse(
                gowiremock.NewResponse().
                    WithStatus(http.StatusOK).
                    WithBody(`{"ok":true}`).
                    WithHeaders(map[string]string{"Content-Type": "application/json"}),
            ),
    ))

    // Your application code hits wm.URL() + "/ping" and receives the stub.
    resp, err := http.Get(wm.URL() + "/ping")
    require.NoError(t, err)
    defer func() { _ = resp.Body.Close() }()
    require.Equal(t, http.StatusOK, resp.StatusCode)
}
```

## Configuration

All `With*` methods are chainable and must be called before `env.Start`.

| Method | Signature | Default | What it controls |
|---|---|---|---|
| `WithImage` | `WithImage(image string) *WireMock` | `"wiremock/wiremock"` | Docker image name. Change this to use a private registry mirror or a custom WireMock distribution. |
| `WithTag` | `WithTag(tag string) *WireMock` | `"3.2.0"` | Docker image tag. Pin to a specific WireMock version or use `"latest"` (not recommended for CI reproducibility). |
| `WithURLPropertyName` | `WithURLPropertyName(name string) *WireMock` | `"<service-name>.url"` | The key under which the base URL is published in `env.Properties()`. Set this to match the config key your application already reads so no extra wiring is needed. |

### Example: pin to a different WireMock version

```go
wm := wiremock.New("wm").
    WithTag("3.3.1")
```

### Example: override the property key

```go
wm := wiremock.New("wm").
    WithURLPropertyName("REMOTE_URL")
```

When the URL property name is overridden, only the custom key is published;
the default `"<name>.url"` key is not emitted.

## Published properties

`Start` returns a single-entry `testrig.Properties` map. The map is also
merged into `env.Properties()` after the env starts.

| Key | Value | Notes |
|---|---|---|
| `<name>.url` (default) or the value passed to `WithURLPropertyName` | `http://<host>:<mapped-port>` | The WireMock base URL reachable from the test process. Port is the Docker-mapped ephemeral port. |

## Typed accessors

These methods are only valid after `env.Start` (or the service's own `Start`)
returns without error. Calling them before `Start` returns empty strings or a
non-functional client.

| Method | Returns | Description |
|---|---|---|
| `URL() string` | Base URL, e.g. `"http://localhost:49175"` | Identical to the value published under the URL property key. |
| `Client() *wiremock.Client` | A `go-wiremock` client pointed at `URL()` | Use for all stub management: `StubFor`, `Reset`, `Verify`, etc. A new `*wiremock.Client` is allocated on each call; there is no shared state in the client object itself. |

`Client()` returns a `*wiremock.Client` from
[`github.com/wiremock/go-wiremock`](https://github.com/wiremock/go-wiremock)
(v1.16.0). The full stubbing API is documented in that module.

## Lifecycle

- **Start** pulls (if needed) and starts the `wiremock/wiremock` container,
  waits for the `/__admin` HTTP endpoint to become healthy (timeout: 60 s),
  resolves the ephemeral mapped port, and returns the URL property.
- **Stop** terminates and removes the container. Runtime state (`container`,
  `url`) is cleared so the instance can be started again. `Stop` is safe to
  call before `Start` or multiple times in a row — it is a no-op when no
  container is running.
- **Restart** is supported: `Stop` followed by `Start` builds a fresh
  container. The new container has an empty stub registry.
- **Double Start** (calling `Start` without an intervening `Stop`) returns an
  error: `wiremock service "<name>" already started`. The existing container is
  not affected.
- **Failure handling**: if `Start` fails after the container is created (e.g.
  host/port resolution errors), the container reference may be left in an
  inconsistent state. Always call `Stop` in a deferred cleanup regardless of
  whether `Start` succeeded.
- The service uses the `slog.Logger` provided by the env (with a
  `service=<name>` attribute already attached). Log lines are emitted at start
  and stop.

## Stubbing patterns

All examples use `wm.Client()` where `wm` is a started `*wiremock.WireMock`
and `gowiremock` is imported as `"github.com/wiremock/go-wiremock"`.

### GET returning JSON

```go
require.NoError(t, wm.Client().StubFor(
    gowiremock.Get(gowiremock.URLPathEqualTo("/api/item")).
        WillReturnResponse(
            gowiremock.NewResponse().
                WithStatus(http.StatusOK).
                WithHeaders(map[string]string{"Content-Type": "application/json"}).
                WithBody(`{"id":1,"name":"widget"}`),
        ),
))
```

### Query-parameter matching

```go
require.NoError(t, wm.Client().StubFor(
    gowiremock.Get(gowiremock.URLPathEqualTo("/lookup")).
        WithQueryParam("key", gowiremock.EqualTo("alpha")).
        WillReturnResponse(
            gowiremock.NewResponse().
                WithStatus(http.StatusOK).
                WithBody(`{"data":"alpha-value"}`),
        ),
))
```

### Reset between tests

WireMock's stub registry persists for the lifetime of the container. When
multiple tests share one env (the recommended pattern via `TestMain`), call
`Reset` at the start of each test that registers stubs:

```go
func resetWireMock(t *testing.T) {
    t.Helper()
    require.NoError(t, wm.Client().Reset())
}

func TestFoo(t *testing.T) {
    resetWireMock(t)
    // register stubs specific to this test ...
}
```

`Reset` removes all stubs and clears the request journal. Tests that do not
register stubs do not need to call it, but calling it defensively is harmless.

## Gaps and workarounds

- **No static mappings or file mounting.** The wrapper does not expose
  testcontainers volume mounts, so you cannot pre-load WireMock JSON mapping
  files from disk. All stubs must be registered programmatically via
  `wm.Client().StubFor(...)` after the env starts.

- **No recording or proxying.** WireMock's record/playback and proxy modes are
  not exposed. If you need recorded fixtures, capture them out-of-band and
  convert to `StubFor` calls, or mount static mappings using a custom
  testcontainers request (not supported by this wrapper).

- **No logger injection.** `WithLogger` is not a setter. The service accepts
  the logger from the env's `Start` call and ignores any logger configured
  before that point.

- **Container image is pinned, not floating.** The default tag is `3.2.0`.
  This is intentional for reproducibility, but it means you must call
  `WithTag` explicitly to pick up a newer WireMock release. Using `"latest"`
  will work but breaks CI reproducibility.

- **Container startup time.** WireMock typically starts in 5–15 seconds on a
  warm Docker daemon. The readiness check polls `/__admin` with a 60 s
  timeout. On a cold daemon or a slow CI runner the first pull can take
  considerably longer; testcontainers does not surface pull progress in the
  test log by default.

- **Shared state across tests.** Because a single container is shared for the
  lifetime of the `TestMain` scope, stubs registered in one test are visible
  to subsequent tests. Always call `wm.Client().Reset()` at the start of each
  test that stubs responses (see [Reset between tests](#reset-between-tests)).

- **No `WithLogger` setter.** There is no way to inject a custom logger before
  `Start`. The logger is provided by the env at `Start` time. If you start the
  service directly (outside an env), pass your logger to `Start` explicitly.

- **`Client()` allocates on every call.** Each invocation of `Client()` returns
  a new `*wiremock.Client`. This is cheap, but if you call it in a tight loop
  you may want to cache the result locally for the duration of a test.

- **No HTTPS support.** The container exposes plain HTTP on port 8080. If your
  application under test requires HTTPS for the remote, you need a proxy or a
  different approach.
