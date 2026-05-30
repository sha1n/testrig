package wiremock

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/sha1n/testrig/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

// syncBuffer is a goroutine-safe sink for slog. streamLogsFrom emits from a
// background goroutine, so the underlying bytes.Buffer must be guarded against
// concurrent Read/Write or the race detector flags it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// newCapturingLogger returns a slog logger that writes (level + message) into
// the returned buffer at Debug level or above.
func newCapturingLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// eventually polls cond every 5ms until it returns true or timeout elapses.
// Returns true if the condition became true within the window. Lets tests
// succeed as soon as the asserted state is observed instead of waiting out a
// fixed sleep, which is both faster and less flaky.
func eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// waitForLog blocks until the buffer contains sentinel or timeout elapses.
func waitForLog(t *testing.T, buf *syncBuffer, sentinel string, timeout time.Duration) {
	t.Helper()
	if !eventually(timeout, func() bool { return strings.Contains(buf.String(), sentinel) }) {
		t.Fatalf("sentinel %q not found within %s; buf:\n%s", sentinel, timeout, buf.String())
	}
}

// ─── argsForVerbose ───────────────────────────────────────────────────────────

func TestArgsForVerbose(t *testing.T) {
	cases := []struct {
		name    string
		verbose bool
		banner  bool
		want    []string
	}{
		{"off, no banner", false, false, nil},
		{"off, banner ignored", false, true, nil},
		{"on, default suppresses banner", true, false, []string{"--verbose", "--disable-banner"}},
		{"on, banner opt-in keeps banner", true, true, []string{"--verbose"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := argsForVerbose(c.verbose, c.banner)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("argsForVerbose(%v, %v): want %v, got %v", c.verbose, c.banner, c.want, got)
			}
		})
	}
}

// ─── streamLogs integration tests ────────────────────────────────────────────

// TestStreamLogs_Integration_RestartsOnCleanEOF exercises the real Docker
// codepath that the unit tests can only simulate: when a container is stopped
// (not terminated), Docker closes the ContainerLogs follow stream with a clean
// EOF. The supervisor's restart loop must detect that EOF and log the debug
// signal — proving end-to-end that the restart codepath fires on the actual
// Docker Desktop symptom this feature exists to handle.
func TestStreamLogs_Integration_RestartsOnCleanEOF(t *testing.T) {
	wm := New("sv-restart").WithVerboseLogging()
	ctx := context.Background()
	logger, buf := newCapturingLogger()
	handle := api.StubEnvHandle("test", logger, nil)

	if _, err := wm.Start(ctx, handle); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = wm.Stop(ctx) }()

	// Wait for the stream to be live before we close it, so the clean-EOF
	// signal we wait for next can only come from the running supervisor —
	// not from a never-attached stream.
	waitForLog(t, buf, "no-request-journal", 30*time.Second)

	// container.Stop (not Terminate) closes the follow stream cleanly while
	// leaving the container around — exactly the Docker Desktop scenario.
	stopTimeout := 5 * time.Second
	if err := wm.container.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("container.Stop: %v", err)
	}

	waitForLog(t, buf, "clean EOF, restarting", 30*time.Second)
}

// ─── fakeContainer ────────────────────────────────────────────────────────────

// fakeContainer is a minimal testcontainers.Container test double. Methods we
// implement explicitly check for a nil function pointer and fail the test with
// a clear "unexpected call to X" message — far easier to diagnose than the
// nil-pointer panic that results from calling a method on the zero-value
// embedded interface.
//
// We still embed the interface so we satisfy the Container method set without
// implementing every one of its 25+ methods; if a test triggers one of those
// it will panic, but that's a much smaller surface than our four wrappers.
type fakeContainer struct {
	testcontainers.Container

	t *testing.T

	getContainerIDFn func() string
	hostFn           func(context.Context) (string, error)
	mappedPortFn     func(context.Context, string) (network.Port, error)
	terminateFn      func(context.Context) error
}

func (f *fakeContainer) GetContainerID() string {
	if f.getContainerIDFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.GetContainerID")
		return ""
	}
	return f.getContainerIDFn()
}

func (f *fakeContainer) Host(ctx context.Context) (string, error) {
	if f.hostFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.Host")
		return "", nil
	}
	return f.hostFn(ctx)
}

func (f *fakeContainer) MappedPort(ctx context.Context, port string) (network.Port, error) {
	if f.mappedPortFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.MappedPort")
		return network.Port{}, nil
	}
	return f.mappedPortFn(ctx, port)
}

func (f *fakeContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	if f.terminateFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.Terminate")
		return nil
	}
	return f.terminateFn(ctx)
}

// ─── Start error-path unit tests (via containerRunFn injection) ───────────────

// TestStart_Unit_HostFailure_CleansUp verifies that when container.Host()
// returns an error, Start returns an error and the cleanup defer terminates the
// container and resets t.container to nil so the service is reusable.
func TestStart_Unit_HostFailure_CleansUp(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
		t:           t,
		hostFn:      func(_ context.Context) (string, error) { return "", errors.New("host unavailable") },
		terminateFn: func(_ context.Context) error { terminated = true; return nil },
	}

	logger, _ := newCapturingLogger()
	wm := New("unit-host-fail")
	wm.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (testcontainers.Container, error) {
		return fake, nil
	}

	_, err := wm.Start(context.Background(), api.StubEnvHandle("test", logger, nil))

	require.Error(t, err, "expected Start to return an error when Host() fails")
	assert.ErrorContains(t, err, "wiremock host")
	assert.True(t, terminated, "container.Terminate should be called when Host() fails")
	assert.Nil(t, wm.container, "wm.container should be nil after failed Start")
}

// TestStart_Unit_MappedPortFailure_CleansUp verifies the same cleanup behaviour
// when MappedPort() fails after Host() succeeds.
func TestStart_Unit_MappedPortFailure_CleansUp(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
		t:      t,
		hostFn: func(_ context.Context) (string, error) { return "localhost", nil },
		mappedPortFn: func(_ context.Context, _ string) (network.Port, error) {
			return network.Port{}, errors.New("no port mapping")
		},
		terminateFn: func(_ context.Context) error { terminated = true; return nil },
	}

	logger, _ := newCapturingLogger()
	wm := New("unit-port-fail")
	wm.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (testcontainers.Container, error) {
		return fake, nil
	}

	_, err := wm.Start(context.Background(), api.StubEnvHandle("test", logger, nil))

	require.Error(t, err, "expected Start to return an error when MappedPort() fails")
	assert.ErrorContains(t, err, "mapped port")
	assert.True(t, terminated, "container.Terminate should be called when MappedPort() fails")
	assert.Nil(t, wm.container, "wm.container should be nil after failed Start")
}

// TestStart_Unit_VerboseHostFailure_CancelsLogGoroutine verifies that when
// verbose logging is enabled and Host() fails, the cleanup defer cancels and
// drains the streaming goroutine — preventing a goroutine leak.
func TestStart_Unit_VerboseHostFailure_CancelsLogGoroutine(t *testing.T) {
	fake := &fakeContainer{
		t:                t,
		getContainerIDFn: func() string { return "fake-cid" },
		hostFn:           func(_ context.Context) (string, error) { return "", errors.New("host unavailable") },
		terminateFn:      func(_ context.Context) error { return nil },
	}

	logger, _ := newCapturingLogger()
	wm := New("unit-verbose-host-fail")
	wm.WithVerboseLogging()
	wm.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (testcontainers.Container, error) {
		return fake, nil
	}

	_, err := wm.Start(context.Background(), api.StubEnvHandle("test", logger, nil))

	require.Error(t, err, "expected Start to return an error when Host() fails")
	assert.False(t, wm.logSupervisor.Running(), "wm.logSupervisor should not be running after failed Start")
}
