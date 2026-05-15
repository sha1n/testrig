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
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
	"github.com/sha1n/testrig"
	"github.com/testcontainers/testcontainers-go"
)

// newCapturingLogger returns a slog logger that writes (level + message) into
// the returned buffer at Debug level or above.
func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
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

// ─── argsForVerbose ───────────────────────────────────────────────────────────

func TestArgsForVerbose_Off_ReturnsNil(t *testing.T) {
	if got := argsForVerbose(false, false); got != nil {
		t.Errorf("verbose=false should yield nil args (no cmd override), got %v", got)
	}
}

func TestArgsForVerbose_OffWithBanner_ReturnsNil(t *testing.T) {
	if got := argsForVerbose(false, true); got != nil {
		t.Errorf("verbose=false+banner=true should yield nil args, got %v", got)
	}
}

func TestArgsForVerbose_OnDefaultBanner_AddsDisableBanner(t *testing.T) {
	want := []string{"--verbose", "--disable-banner"}
	got := argsForVerbose(true, false)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("verbose=true,banner=false: want %v, got %v", want, got)
	}
}

func TestArgsForVerbose_OnWithBanner_OmitsDisableBanner(t *testing.T) {
	want := []string{"--verbose"}
	got := argsForVerbose(true, true)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("verbose=true,banner=true: want %v, got %v", want, got)
	}
}

// ─── lineWriter ───────────────────────────────────────────────────────────────

func TestLineWriter_LogsAtInfo(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("hello"))

	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("should log at INFO; got: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output should contain the message; got: %q", out)
	}
}

func TestLineWriter_LogsAtWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelWarn}

	_, _ = w.Write([]byte("oops"))

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("should log at WARN; got: %q", out)
	}
}

func TestLineWriter_TrimsTrailingNewline(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("line\n"))

	if !strings.Contains(buf.String(), "msg=line") {
		t.Errorf("trailing \\n should be trimmed; got: %q", buf.String())
	}
}

func TestLineWriter_TrimsTrailingCRLF(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("line\r\n"))

	if !strings.Contains(buf.String(), "msg=line") {
		t.Errorf("trailing \\r\\n should be trimmed; got: %q", buf.String())
	}
}

func TestLineWriter_EmptyContent_NoLog(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte(""))

	if buf.Len() != 0 {
		t.Errorf("empty content must not emit a log line; got: %q", buf.String())
	}
}

func TestLineWriter_NewlineOnlyContent_NoLog(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("\r\n"))

	if buf.Len() != 0 {
		t.Errorf("newline-only content must not emit a log line; got: %q", buf.String())
	}
}

// ─── streamLogsFrom unit tests ────────────────────────────────────────────────

func TestStreamLogsFrom_Unit_LogsStdout(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-stdout"}

	fake := &fakeLogStreamOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return makeMultiplexedStream("hello from stdout\n", ""), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	// After the one-shot stream drains, cancel to stop the restart loop.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(buf.String(), "hello from stdout") {
		t.Errorf("stdout should be logged; got:\n%s", buf.String())
	}
}

func TestStreamLogsFrom_Unit_LogsStderrAtWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-stderr"}

	fake := &fakeLogStreamOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return makeMultiplexedStream("", "error output\n"), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	out := buf.String()
	if !strings.Contains(out, "error output") {
		t.Errorf("stderr should be logged; got:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("stderr should be logged at WARN; got:\n%s", out)
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(context.Background(), "cid", fake)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogsFrom did not return after ContainerLogs() failure")
	}

	if !strings.Contains(buf.String(), "log stream failed to open") {
		t.Errorf("expected warn about open failure; got:\n%s", buf.String())
	}
}

func TestStreamLogsFrom_Unit_RestartsOnCleanEOF(t *testing.T) {
	logger, buf := newCapturingLogger()
	wm := &WireMock{logger: logger, name: "unit-restart"}

	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			calls++
			if calls == 1 {
				// First call: return a stream that immediately exhausts (clean EOF).
				return makeMultiplexedStream("", ""), nil
			}
			// Second call: block until ctx is cancelled, simulating a running container.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	// Allow time for the clean-EOF path and restart.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogsFrom did not return after context cancel")
	}

	if !strings.Contains(buf.String(), "clean EOF") {
		t.Errorf("expected debug log about clean EOF restart; got:\n%s", buf.String())
	}
	if calls < 2 {
		t.Errorf("expected ContainerLogs() to be called at least twice (initial + restart); got %d", calls)
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(context.Background(), "cid", fake)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogsFrom did not return after stream error")
	}

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

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogsFrom did not return when context was pre-cancelled")
	}

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

	ctx, cancel := context.WithCancel(context.Background())

	fake := &fakeLogStreamOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			pr, pw := io.Pipe()
			// Close the pipe when the ctx is cancelled so stdcopy.StdCopy unblocks.
			go func() {
				<-ctx.Done()
				_ = pw.CloseWithError(ctx.Err())
			}()
			return pr, nil
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wm.streamLogsFrom(ctx, "cid", fake)
	}()

	time.Sleep(50 * time.Millisecond) // let StdCopy enter its blocking Read
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogsFrom did not return after ctx cancel during stream")
	}

	if strings.Contains(buf.String(), "log stream error") {
		t.Errorf("no warning should be logged on ctx-cancel stream error; got:\n%s", buf.String())
	}
}

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

// TestLineWriter_MultiLineContentLogsEntireFrame documents that a single Write
// call (one Docker frame) containing embedded newlines is emitted as one slog
// entry — trailing newline trimmed, internal newlines preserved. Docker frames
// are normally one line each, but this pins the current behaviour so a future
// refactor can't silently change it.
func TestLineWriter_MultiLineContentLogsEntireFrame(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("line1\nline2\n"))

	if count := strings.Count(buf.String(), "level=INFO"); count != 1 {
		t.Errorf("multi-line Write should produce exactly one log entry; got %d entries:\n%s", count, buf.String())
	}
	if !strings.Contains(buf.String(), "line1") || !strings.Contains(buf.String(), "line2") {
		t.Errorf("both lines should appear in the single log entry; got:\n%s", buf.String())
	}
}

// ─── streamLogs integration tests ────────────────────────────────────────────

func TestStreamLogs_Integration_RestartsOnCleanEOF(t *testing.T) {
	wm := New("sv-restart").WithVerboseLogging()
	ctx := context.Background()
	logger, _ := newCapturingLogger()
	handle := testrig.StubEnvHandle("test", logger, nil)

	if _, err := wm.Start(ctx, handle); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = wm.Stop(ctx) }()

	// Stop the container (not terminate). Docker closes the ContainerLogs
	// --follow stream with a clean EOF. streamLogsFrom detects the nil error
	// from stdcopy.StdCopy, logs a debug message, and calls ContainerLogs again.
	stopTimeout := 5 * time.Second
	if err := wm.container.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("container.Stop: %v", err)
	}

	// Allow the supervisor to receive the clean-EOF signal and attempt a restart.
	time.Sleep(2 * time.Second)
}

// ─── fakeContainer ────────────────────────────────────────────────────────────

// fakeContainer is a minimal test double for testcontainers.Container.
// Unimplemented methods are provided by the embedded interface (nil value) and
// will panic if called — acceptable because our tests only exercise the four
// methods Start actually calls.
type fakeContainer struct {
	testcontainers.Container
	getContainerIDFn func() string
	hostFn           func(context.Context) (string, error)
	mappedPortFn     func(context.Context, string) (network.Port, error)
	terminateFn      func(context.Context) error
}

func (f *fakeContainer) GetContainerID() string { return f.getContainerIDFn() }
func (f *fakeContainer) Host(ctx context.Context) (string, error) {
	return f.hostFn(ctx)
}
func (f *fakeContainer) MappedPort(ctx context.Context, port string) (network.Port, error) {
	return f.mappedPortFn(ctx, port)
}
func (f *fakeContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	return f.terminateFn(ctx)
}

// ─── Start error-path unit tests (via containerRunFn injection) ───────────────

// TestStart_Unit_HostFailure_CleansUp verifies that when container.Host()
// returns an error, Start returns an error and the cleanup defer terminates the
// container and resets t.container to nil so the service is reusable.
func TestStart_Unit_HostFailure_CleansUp(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
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
// verbose logging is enabled and Host() fails, the cleanup defer calls
// t.logStop() and clears it to nil — preventing a goroutine leak.
func TestStart_Unit_VerboseHostFailure_CancelsLogGoroutine(t *testing.T) {
	fake := &fakeContainer{
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
	// Prevent the log goroutine from hitting a real Docker daemon; we only
	// care that logStop is called — not that the goroutine does anything useful.
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
}

// TestStreamLogs_Unit_DockerClientCreateFailure covers line 246: when
// dockerclient.New fails, streamLogs logs a warning and returns immediately.
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

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamLogs did not return after Docker client creation failure")
	}

	if !strings.Contains(buf.String(), "failed to create Docker client") {
		t.Errorf("expected warn about client creation failure; got:\n%s", buf.String())
	}
}
