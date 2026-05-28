# dockerlog

Shared Docker container log streaming for testrig services.

`Supervisor` manages a background goroutine that forwards a container's
stdout and stderr into a `slog.Logger`. It handles the restart-on-EOF
behaviour seen on Docker Desktop (macOS) and shuts down gracefully when
the container terminates.

## Usage

```go
import "github.com/sha1n/testrig/pkg/dockerlog"

type MyService struct {
    logSupervisor dockerlog.Supervisor
    streaming     bool
    // ...
}

func (s *MyService) WithLogStreaming() *MyService {
    s.streaming = true
    return s
}

func (s *MyService) Start(ctx context.Context, env testrig.EnvHandle) (testrig.Properties, error) {
    // ... start container ...

    if s.streaming {
        s.logSupervisor.Start(s.container.GetContainerID(), env.Logger())
    }
    // ...
}

func (s *MyService) Stop(ctx context.Context) error {
    s.logSupervisor.Cancel()          // cancel BEFORE Terminate
    err := s.container.Terminate(ctx) // unblocks the in-flight ContainerLogs read
    s.logSupervisor.Wait()            // drain AFTER Terminate
    return err
}
```

## Supervisor API

| Method | Description |
|--------|-------------|
| `Start(containerID, logger)` | Spawn the streaming goroutine. No-op if already running. |
| `Cancel()` | Cancel the goroutine's context. Call **before** `container.Terminate()`. |
| `Wait()` | Block until the goroutine exits (or `StopWait` elapses). Call **after** `container.Terminate()`. |
| `Running() bool` | Report whether the goroutine is active. |

**Tunables** (set before `Start`):

| Field | Default | Description |
|-------|---------|-------------|
| `RestartBackoff` | 500 ms | Pause between ContainerLogs calls after a clean EOF. |
| `StopWait` | 5 s | Maximum time `Wait` blocks before logging a warning and returning. On timeout, the streaming goroutine — and a small watchdog that waits to clear its state — may remain alive until process exit if the underlying Docker call is wedged. The warning surfaces this. |
| `NewOpenerFn` | nil (real Docker client) | Override the opener factory — inject a fake in tests to avoid a real daemon. |

## Why the Cancel → Terminate → Wait ordering matters

`Wait` blocks on the ContainerLogs HTTP stream. Only `container.Terminate`
closes that stream and unblocks the read. `Cancel` must come first so the
goroutine is in shutdown state before the unblock arrives; otherwise it
would attempt a restart.

## Caveat: restart `since` is wall-clock

After a clean EOF the supervisor reopens the stream with
`Since: time.Now().UTC()`. The Docker daemon interprets that against its
own clock, so significant client/daemon clock skew can cause duplicate
entries (client clock behind) or a small log gap (client clock ahead)
across each restart. Acceptable for a test rig; not suitable as a
durable log-shipping layer.
