package wiremock

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
	"github.com/sha1n/testrig"
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

// makeMultiplexedStream encodes stdout and/or stderr strings into a Docker
// multiplexed stream (the format returned by ContainerLogs for non-TTY containers).
// Frame format: [stream-type(1)] [zeros(3)] [size-big-endian(4)] [payload(N)]
func makeMultiplexedStream(stdout, stderr string) io.ReadCloser {
	var buf bytes.Buffer
	writeFrame := func(streamType byte, payload string) {
		if payload == "" {
			return
		}
		header := make([]byte, 8)
		header[0] = streamType
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		buf.Write(header)
		buf.WriteString(payload)
	}
	writeFrame(1, stdout) // 1 = stdout
	writeFrame(2, stderr) // 2 = stderr
	return io.NopCloser(&buf)
}

// fakeLogStreamOpener is a test double for logStreamOpener. It lets unit tests
// exercise streamLogsFrom branches without a live Docker daemon.
type fakeLogStreamOpener struct {
	logsFn func(context.Context, string, dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
}

func (f *fakeLogStreamOpener) ContainerLogs(ctx context.Context, containerID string, opts dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
	return f.logsFn(ctx, containerID, opts)
}

// startStreamLogs launches streamLogsFrom in a goroutine and returns a cancel
// func plus a channel closed when the goroutine exits. Tests use this to keep
// goroutine lifecycle obvious — no leaked goroutines, no fixed sleeps.
func startStreamLogs(wm *WireMock, opener logStreamOpener) (cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancelFn := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		wm.streamLogsFrom(ctx, "cid", opener)
	}()
	return cancelFn, doneCh
}

// awaitDone fails the test if done isn't closed within timeout.
func awaitDone(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("goroutine did not return within %s", timeout)
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

// ─── lineWriter ───────────────────────────────────────────────────────────────

// TestLineWriter covers level routing, newline trimming, empty-content
// dropping, and embedded-newline splitting. A single Docker frame containing
// embedded newlines must produce one slog entry per non-empty line so
// downstream renderers don't garble multi-line bursts.
func TestLineWriter(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		level   slog.Level
		want    []string // substrings that must each appear exactly once
		wantNot []string // substrings that must not appear
		count   int      // expected number of slog entries (0 ⇒ no log)
	}{
		{
			name:  "info level",
			input: "hello",
			level: slog.LevelInfo,
			want:  []string{"level=INFO", "hello"},
			count: 1,
		},
		{
			name:  "warn level",
			input: "oops",
			level: slog.LevelWarn,
			want:  []string{"level=WARN", "oops"},
			count: 1,
		},
		{
			name:  "trailing LF trimmed",
			input: "line\n",
			level: slog.LevelInfo,
			want:  []string{"msg=line"},
			count: 1,
		},
		{
			name:  "trailing CRLF trimmed",
			input: "line\r\n",
			level: slog.LevelInfo,
			want:  []string{"msg=line"},
			count: 1,
		},
		{
			name:  "empty content suppressed",
			input: "",
			level: slog.LevelInfo,
			count: 0,
		},
		{
			name:  "newline-only content suppressed",
			input: "\r\n",
			level: slog.LevelInfo,
			count: 0,
		},
		{
			name:  "multi-line frame splits into separate entries",
			input: "line1\nline2\n",
			level: slog.LevelInfo,
			want:  []string{"msg=line1", "msg=line2"},
			count: 2,
		},
		{
			name:    "multi-line skips empty interior lines",
			input:   "first\n\nsecond\n",
			level:   slog.LevelInfo,
			want:    []string{"msg=first", "msg=second"},
			wantNot: []string{"msg=\n"},
			count:   2,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logger, buf := newCapturingLogger()
			w := &lineWriter{logger: logger, level: c.level}

			n, err := w.Write([]byte(c.input))
			if err != nil {
				t.Fatalf("Write returned error: %v", err)
			}
			if n != len(c.input) {
				t.Errorf("Write should report all bytes consumed; want %d, got %d", len(c.input), n)
			}

			out := buf.String()
			if c.count == 0 {
				if buf.Len() != 0 {
					t.Errorf("expected no log entries; got: %q", out)
				}
				return
			}

			// slog's TextHandler emits one "msg=" per entry, quoted or not.
			gotCount := strings.Count(out, "msg=")
			if gotCount != c.count {
				t.Errorf("expected %d log entries; got %d. buf:\n%s", c.count, gotCount, out)
			}
			for _, s := range c.want {
				if !strings.Contains(out, s) {
					t.Errorf("expected output to contain %q; got:\n%s", s, out)
				}
			}
			for _, s := range c.wantNot {
				if strings.Contains(out, s) {
					t.Errorf("output should not contain %q; got:\n%s", s, out)
				}
			}
		})
	}
}

// ─── streamLogsFrom unit tests ────────────────────────────────────────────────

// TestStreamLogsFrom_Unit_RoutesByStream covers stdout→INFO and stderr→WARN
// routing through the multiplexed stream. Combined into one table to avoid
// duplicating the start/wait/cancel scaffolding.
func TestStreamLogsFrom_Unit_RoutesByStream(t *testing.T) {
	cases := []struct {
		name      string
		stdout    string
		stderr    string
		wantMsg   string
		wantLevel string
	}{
		{"stdout → INFO", "hello from stdout\n", "", "hello from stdout", "level=INFO"},
		{"stderr → WARN", "", "error output\n", "error output", "level=WARN"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logger, buf := newCapturingLogger()
			wm := &WireMock{logger: logger, name: "unit-route"}

			// Open once, return immediately on the next iteration to keep the
			// supervisor parked until we cancel.
			calls := 0
			fake := &fakeLogStreamOpener{
				logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
					calls++
					if calls == 1 {
						return makeMultiplexedStream(c.stdout, c.stderr), nil
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}

			cancel, done := startStreamLogs(wm, fake)

			waitForLog(t, buf, c.wantMsg, time.Second)
			if !strings.Contains(buf.String(), c.wantLevel) {
				t.Errorf("expected level %s; got:\n%s", c.wantLevel, buf.String())
			}

			cancel()
			awaitDone(t, done, time.Second)
		})
	}
}

func TestStreamLogsFrom_Unit_OpenFailure(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-open-fail"}

	fake := &fakeLogStreamOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return nil, errors.New("cannot open log stream")
		},
	}

	_, done := startStreamLogs(wm, fake)
	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "log stream failed to open") {
		t.Errorf("expected warn about open failure; got:\n%s", buf.String())
	}
}

func TestStreamLogsFrom_Unit_RestartsOnCleanEOF(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-restart"}

	var callsMu sync.Mutex
	calls := 0
	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			callsMu.Lock()
			calls++
			callsMu.Unlock()
			// First call delivers a stream that immediately exhausts (clean EOF).
			// Subsequent calls block until ctx is cancelled — simulating a
			// healthy follow stream awaiting more container output.
			if calls == 1 {
				return makeMultiplexedStream("", ""), nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	cancel, done := startStreamLogs(wm, fake)

	// Wait for evidence of the restart path: the debug log and a 2nd
	// ContainerLogs call. Polling drains as soon as both observations land.
	waitForLog(t, buf, "clean EOF", time.Second)
	if !eventually(time.Second, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return calls >= 2
	}) {
		t.Fatalf("ContainerLogs() was not called twice within timeout (got %d)", calls)
	}

	cancel()
	awaitDone(t, done, time.Second)
}

func TestStreamLogsFrom_Unit_RestartUsesSinceTimestamp(t *testing.T) {
	logger, _ := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-since"}

	var (
		mu          sync.Mutex
		seenOptions []dockerclient.ContainerLogsOptions
	)

	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, opts dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			mu.Lock()
			seenOptions = append(seenOptions, opts)
			n := len(seenOptions)
			mu.Unlock()

			if n == 1 {
				return makeMultiplexedStream("", ""), nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	cancel, done := startStreamLogs(wm, fake)

	if !eventually(time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seenOptions) >= 2
	}) {
		mu.Lock()
		t.Fatalf("expected at least 2 ContainerLogs calls; got %d", len(seenOptions))
	}

	cancel()
	awaitDone(t, done, time.Second)

	mu.Lock()
	defer mu.Unlock()
	if seenOptions[0].Since != "" {
		t.Errorf("first call should not set Since (need full history); got %q", seenOptions[0].Since)
	}
	if seenOptions[1].Since == "" {
		t.Errorf("second call must set Since to bound duplication on restart")
	}
	if _, err := time.Parse(time.RFC3339Nano, seenOptions[1].Since); err != nil {
		t.Errorf("Since on restart should be RFC3339Nano; got %q (%v)", seenOptions[1].Since, err)
	}
}

func TestStreamLogsFrom_Unit_ExitsOnStreamError(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-stream-err"}

	// Return a reader whose first byte is not a valid Docker stream-type header
	// byte (not 0–3) so stdcopy returns an error.
	fake := &fakeLogStreamOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return io.NopCloser(strings.NewReader("not a valid docker multiplex stream")), nil
		},
	}

	_, done := startStreamLogs(wm, fake)
	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "log stream error") {
		t.Errorf("expected warn about stream error; got:\n%s", buf.String())
	}
}

// TestStreamLogsFrom_Unit_CtxCancelledOpenError_NoWarn covers the branch where
// ContainerLogs returns because the supervisor ctx was already cancelled. The
// code must exit silently — no warning should be logged, because a cancelled
// ctx is a normal shutdown signal, not an error.
func TestStreamLogsFrom_Unit_CtxCancelledOpenError_NoWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-ctx-cancel-open"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: simulates Stop() being called before the loop even starts

	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return nil, ctx.Err()
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	awaitDone(t, done, time.Second)

	if strings.Contains(buf.String(), "log stream failed to open") {
		t.Errorf("no warning should be logged on ctx-cancel open error; got:\n%s", buf.String())
	}
}

// TestStreamLogsFrom_Unit_CtxCancelledDuringStream_NoWarn covers the branch
// where the supervisor ctx is cancelled while stdcopy.StdCopy is blocking on a
// live read. The pipe closes with context.Canceled, StdCopy returns a non-nil
// error, but ctx.Err() != nil so the function must exit cleanly without logging
// a warning.
func TestStreamLogsFrom_Unit_CtxCancelledDuringStream_NoWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-ctx-cancel-stream"}

	// readEntered fires when the fake reader's first Read call lands —
	// proof that StdCopy is now blocked on it. Lets us cancel exactly at
	// that moment instead of guessing with a fixed Sleep.
	readEntered := make(chan struct{})

	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			pr, pw := io.Pipe()
			rc := &signallingReadCloser{r: pr, entered: readEntered}
			go func() {
				<-ctx.Done()
				_ = pw.CloseWithError(ctx.Err())
			}()
			return rc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	select {
	case <-readEntered:
	case <-time.After(time.Second):
		t.Fatal("StdCopy never entered its blocking Read")
	}
	cancel()

	awaitDone(t, done, time.Second)

	if strings.Contains(buf.String(), "log stream error") {
		t.Errorf("no warning should be logged on ctx-cancel stream error; got:\n%s", buf.String())
	}
}

// signallingReadCloser closes `entered` on the first Read call so tests can
// synchronize on "StdCopy is now blocked on Read" instead of a fixed sleep.
type signallingReadCloser struct {
	r        io.ReadCloser
	entered  chan struct{}
	enteredO sync.Once
}

func (s *signallingReadCloser) Read(p []byte) (int, error) {
	s.enteredO.Do(func() { close(s.entered) })
	return s.r.Read(p)
}

func (s *signallingReadCloser) Close() error { return s.r.Close() }

// TestStreamLogsFrom_Unit_ContainerLogsOptions verifies that every call to
// ContainerLogs passes Follow: true (required for live streaming), ShowStdout:
// true, ShowStderr: true, and forwards the containerID unchanged.
func TestStreamLogsFrom_Unit_ContainerLogsOptions(t *testing.T) {
	wm := &WireMock{logger: slog.Default(), name: "unit-options"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // exit after the first call so we can inspect what was passed

	var capturedID string
	var capturedOpts dockerclient.ContainerLogsOptions

	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, containerID string, opts dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			capturedID = containerID
			capturedOpts = opts
			return nil, ctx.Err()
		},
	}

	wm.streamLogsFrom(ctx, "test-container-id", fake)

	if capturedID != "test-container-id" {
		t.Errorf("containerID not forwarded; want %q, got %q", "test-container-id", capturedID)
	}
	if !capturedOpts.Follow {
		t.Error("ContainerLogs must be called with Follow: true for live streaming")
	}
	if !capturedOpts.ShowStdout {
		t.Error("ContainerLogs must be called with ShowStdout: true")
	}
	if !capturedOpts.ShowStderr {
		t.Error("ContainerLogs must be called with ShowStderr: true")
	}
}

// ─── streamLogs (Docker client wiring) ────────────────────────────────────────

// TestStreamLogs_Unit_DockerClientCreateFailure covers the dockerclient.New
// failure path: when the constructor errors, streamLogs logs a warning and
// returns without calling into the opener.
func TestStreamLogs_Unit_DockerClientCreateFailure(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{
		logger:            logger,
		name:              "unit-client-fail",
		newDockerClientFn: func() (*dockerclient.Client, error) { return nil, errors.New("no docker socket") },
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogs(context.Background(), "cid")
	}()

	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "failed to create Docker client") {
		t.Errorf("expected warn about client creation failure; got:\n%s", buf.String())
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
	handle := testrig.StubEnvHandle("test", logger, nil)

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

	_, err := wm.Start(context.Background(), testrig.StubEnvHandle("test", logger, nil))

	if err == nil {
		t.Fatal("expected Start to return an error when Host() fails")
	}
	if !strings.Contains(err.Error(), "wiremock host") {
		t.Errorf("error message should mention host; got: %v", err)
	}
	if !terminated {
		t.Error("container.Terminate should be called when Host() fails")
	}
	if wm.container != nil {
		t.Error("wm.container should be nil after failed Start")
	}
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

	_, err := wm.Start(context.Background(), testrig.StubEnvHandle("test", logger, nil))

	if err == nil {
		t.Fatal("expected Start to return an error when MappedPort() fails")
	}
	if !strings.Contains(err.Error(), "mapped port") {
		t.Errorf("error message should mention mapped port; got: %v", err)
	}
	if !terminated {
		t.Error("container.Terminate should be called when MappedPort() fails")
	}
	if wm.container != nil {
		t.Error("wm.container should be nil after failed Start")
	}
}

// TestStart_Unit_VerboseHostFailure_CancelsLogGoroutine verifies that when
// verbose logging is enabled and Host() fails, the cleanup defer cancels and
// drains the streaming goroutine — preventing a goroutine leak. We stub the
// Docker client constructor to return an error so the goroutine returns
// promptly without needing a real daemon.
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
	wm.newDockerClientFn = func() (*dockerclient.Client, error) {
		return nil, errors.New("no docker in unit test")
	}

	_, err := wm.Start(context.Background(), testrig.StubEnvHandle("test", logger, nil))

	if err == nil {
		t.Fatal("expected Start to return an error when Host() fails")
	}
	if wm.logStop != nil {
		t.Error("wm.logStop should be nil after failed Start — cleanup must have called and cleared it")
	}
	if wm.logDone != nil {
		t.Error("wm.logDone should be nil after failed Start — cleanup must have waited and cleared it")
	}
}
