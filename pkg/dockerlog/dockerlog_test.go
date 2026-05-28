package dockerlog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
)

// ─── test helpers ──────────────────────────────────────────────────────────────

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

func newCapturingLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

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

func waitForLog(t *testing.T, buf *syncBuffer, sentinel string, timeout time.Duration) {
	t.Helper()
	if !eventually(timeout, func() bool { return strings.Contains(buf.String(), sentinel) }) {
		t.Fatalf("sentinel %q not found within %s; buf:\n%s", sentinel, timeout, buf.String())
	}
}

func awaitDone(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("goroutine did not return within %s", timeout)
	}
}

// firstCallBranch implements the counter pattern used by fakeOpener closures:
// it atomically increments *calls under mu and reports whether this invocation
// was the first. Returning a snapshot taken inside the lock is required for
// concurrency safety.
func firstCallBranch(mu *sync.Mutex, calls *int) bool {
	mu.Lock()
	*calls++
	n := *calls
	mu.Unlock()
	return n == 1
}

// makeMultiplexedStream encodes stdout/stderr into Docker's multiplexed frame format.
// Frame: [stream-type(1)] [zeros(3)] [size-big-endian(4)] [payload(N)]
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

type fakeOpener struct {
	logsFn func(context.Context, string, dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
}

func (f *fakeOpener) ContainerLogs(ctx context.Context, containerID string, opts dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
	return f.logsFn(ctx, containerID, opts)
}

// startStreamFrom launches s.streamFrom in a goroutine and returns a cancel func
// plus a channel closed when the goroutine exits. The supervisor's logger and
// RestartBackoff are read once here and passed as parameters, matching how
// Start invokes the goroutine.
func startStreamFrom(s *Supervisor, o opener) (cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancelFn := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s.streamFrom(ctx, "cid", o, s.logger, s.RestartBackoff)
	}()
	return cancelFn, doneCh
}

// signallingReadCloser closes `entered` on the first Read call so tests can
// synchronize on "StdCopy is now blocked on Read" rather than sleeping.
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

// ─── lineWriter ────────────────────────────────────────────────────────────────

func TestLineWriter_InfoLevel(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	input := []byte("hello\n")
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write should consume all bytes; want %d, got %d", len(input), n)
	}
	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected level=INFO; got:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected message 'hello'; got:\n%s", out)
	}
}

func TestLineWriter_WarnLevel(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelWarn}
	_, _ = w.Write([]byte("oops\n"))
	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected level=WARN; got:\n%s", out)
	}
	if !strings.Contains(out, "oops") {
		t.Errorf("expected message 'oops'; got:\n%s", out)
	}
}

func TestLineWriter_TrailingLFTrimmed(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	_, _ = w.Write([]byte("line\n"))
	if !strings.Contains(buf.String(), "msg=line") {
		t.Errorf("trailing LF should be trimmed; got:\n%s", buf.String())
	}
}

func TestLineWriter_TrailingCRLFTrimmed(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	_, _ = w.Write([]byte("line\r\n"))
	if !strings.Contains(buf.String(), "msg=line") {
		t.Errorf("trailing CRLF should be trimmed; got:\n%s", buf.String())
	}
}

func TestLineWriter_EmptyContentSuppressed(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	n, err := w.Write([]byte(""))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 0 {
		t.Errorf("Write should report 0 bytes for empty input; got %d", n)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log entries for empty input; got:\n%s", buf.String())
	}
}

func TestLineWriter_NewlineOnlyContentSuppressed(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	_, _ = w.Write([]byte("\r\n"))
	if buf.Len() != 0 {
		t.Errorf("expected no log entries for newline-only input; got:\n%s", buf.String())
	}
}

func TestLineWriter_MultiLineFrameSplits(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	_, _ = w.Write([]byte("line1\nline2\n"))
	out := buf.String()
	if !strings.Contains(out, "msg=line1") {
		t.Errorf("expected msg=line1; got:\n%s", out)
	}
	if !strings.Contains(out, "msg=line2") {
		t.Errorf("expected msg=line2; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 2 {
		t.Errorf("expected 2 log entries; got %d:\n%s", count, out)
	}
}

func TestLineWriter_MultiLineSkipsEmptyInteriorLines(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	_, _ = w.Write([]byte("first\n\nsecond\n"))
	out := buf.String()
	if !strings.Contains(out, "msg=first") {
		t.Errorf("expected msg=first; got:\n%s", out)
	}
	if !strings.Contains(out, "msg=second") {
		t.Errorf("expected msg=second; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 2 {
		t.Errorf("expected 2 log entries (empty line skipped); got %d:\n%s", count, out)
	}
}

func TestLineWriter_PartialLineBufferedAcrossWrites(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}

	// First Write delivers a line fragment with no terminator. Nothing should
	// be emitted yet — the writer must buffer until it sees the line end.
	_, _ = w.Write([]byte("hello "))
	if buf.Len() != 0 {
		t.Fatalf("expected no log entries before newline; got:\n%s", buf.String())
	}

	// Second Write completes the line. Exactly one entry should appear.
	_, _ = w.Write([]byte("world\n"))
	out := buf.String()
	if !strings.Contains(out, "msg=\"hello world\"") {
		t.Errorf("expected joined msg=\"hello world\"; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 1 {
		t.Errorf("expected 1 log entry; got %d:\n%s", count, out)
	}
}

func TestLineWriter_MultipleLinesWithPartialTrailingFragment(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}

	// "a\nb\nc" — two complete lines and one partial fragment.
	_, _ = w.Write([]byte("a\nb\nc"))
	out := buf.String()
	if !strings.Contains(out, "msg=a") || !strings.Contains(out, "msg=b") {
		t.Errorf("expected msg=a and msg=b; got:\n%s", out)
	}
	if strings.Contains(out, "msg=c") {
		t.Errorf("partial fragment 'c' should not be emitted yet; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 2 {
		t.Errorf("expected 2 entries before fragment completes; got %d:\n%s", count, out)
	}

	// Complete the fragment.
	_, _ = w.Write([]byte("d\n"))
	out = buf.String()
	if !strings.Contains(out, "msg=cd") {
		t.Errorf("expected joined msg=cd; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 3 {
		t.Errorf("expected 3 entries total; got %d:\n%s", count, out)
	}
}

func TestLineWriter_Flush_EmitsBufferedFragment(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}

	_, _ = w.Write([]byte("tail"))
	if buf.Len() != 0 {
		t.Fatalf("expected nothing logged before flush; got:\n%s", buf.String())
	}
	w.Flush()
	out := buf.String()
	if !strings.Contains(out, "msg=tail") {
		t.Errorf("expected msg=tail after flush; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 1 {
		t.Errorf("expected 1 entry; got %d:\n%s", count, out)
	}

	// Flush again — buffer is empty, nothing more should appear.
	w.Flush()
	if count := strings.Count(buf.String(), "msg="); count != 1 {
		t.Errorf("Flush on empty buffer should be a no-op; got %d entries", count)
	}
}

func TestLineWriter_Flush_NoOpWhenEmpty(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	w.Flush()
	if buf.Len() != 0 {
		t.Errorf("Flush with empty buffer should not log; got:\n%s", buf.String())
	}
}

func TestLineWriter_CRLFAcrossWrites(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	// CR arrives at the end of one Write, LF at the start of the next.
	_, _ = w.Write([]byte("windows\r"))
	_, _ = w.Write([]byte("\nnext\n"))
	out := buf.String()
	if !strings.Contains(out, "msg=windows") {
		t.Errorf("expected msg=windows with CR trimmed; got:\n%s", out)
	}
	if !strings.Contains(out, "msg=next") {
		t.Errorf("expected msg=next; got:\n%s", out)
	}
	if count := strings.Count(out, "msg="); count != 2 {
		t.Errorf("expected 2 entries; got %d:\n%s", count, out)
	}
}

// ─── Supervisor.streamFrom ─────────────────────────────────────────────────────

func TestSupervisor_RoutesByStream_StdoutToInfo(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger, RestartBackoff: 10 * time.Millisecond}

	var (
		callsMu sync.Mutex
		calls   int
	)
	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			if firstCallBranch(&callsMu, &calls) {
				return makeMultiplexedStream("hello from stdout\n", ""), nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	cancel, done := startStreamFrom(s, fake)
	waitForLog(t, buf, "hello from stdout", time.Second)
	if !strings.Contains(buf.String(), "level=INFO") {
		t.Errorf("stdout should be logged at INFO; got:\n%s", buf.String())
	}
	cancel()
	awaitDone(t, done, time.Second)
}

func TestSupervisor_RoutesByStream_StderrToWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger, RestartBackoff: 10 * time.Millisecond}

	var (
		callsMu sync.Mutex
		calls   int
	)
	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			if firstCallBranch(&callsMu, &calls) {
				return makeMultiplexedStream("", "error output\n"), nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	cancel, done := startStreamFrom(s, fake)
	waitForLog(t, buf, "error output", time.Second)
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("stderr should be logged at WARN; got:\n%s", buf.String())
	}
	cancel()
	awaitDone(t, done, time.Second)
}

func TestSupervisor_OpenFailure_LogsWarnAndExits(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger}

	fake := &fakeOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return nil, errors.New("cannot open log stream")
		},
	}

	_, done := startStreamFrom(s, fake)
	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "log stream failed to open") {
		t.Errorf("expected warn about open failure; got:\n%s", buf.String())
	}
}

func TestSupervisor_RestartsOnCleanEOF(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger, RestartBackoff: 10 * time.Millisecond}

	var callsMu sync.Mutex
	calls := 0
	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			if firstCallBranch(&callsMu, &calls) {
				return makeMultiplexedStream("", ""), nil // immediate clean EOF
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	cancel, done := startStreamFrom(s, fake)

	waitForLog(t, buf, "clean EOF", time.Second)
	if !eventually(time.Second, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return calls >= 2
	}) {
		callsMu.Lock()
		n := calls
		callsMu.Unlock()
		t.Fatalf("expected at least 2 ContainerLogs calls; got %d", n)
	}

	cancel()
	awaitDone(t, done, time.Second)
}

func TestSupervisor_RestartUsesSinceTimestamp(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{logger: logger, RestartBackoff: 10 * time.Millisecond}

	var (
		mu          sync.Mutex
		seenOptions []dockerclient.ContainerLogsOptions
	)

	fake := &fakeOpener{
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

	cancel, done := startStreamFrom(s, fake)

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
		t.Errorf("first call should not set Since; got %q", seenOptions[0].Since)
	}
	if seenOptions[1].Since == "" {
		t.Error("second call must set Since to bound duplicate entries on restart")
	}
	if _, err := time.Parse(time.RFC3339Nano, seenOptions[1].Since); err != nil {
		t.Errorf("Since should be RFC3339Nano; got %q (%v)", seenOptions[1].Since, err)
	}
}

func TestSupervisor_ExitsOnStreamError_LogsWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger}

	fake := &fakeOpener{
		logsFn: func(_ context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return io.NopCloser(strings.NewReader("not a valid docker multiplex stream")), nil
		},
	}

	_, done := startStreamFrom(s, fake)
	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "log stream error") {
		t.Errorf("expected warn about stream error; got:\n%s", buf.String())
	}
}

func TestSupervisor_CtxCancelledOnOpen_NoWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return nil, ctx.Err()
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.streamFrom(ctx, "cid", fake, s.logger, 0)
	}()

	awaitDone(t, done, time.Second)

	if strings.Contains(buf.String(), "log stream failed to open") {
		t.Errorf("no warning should be logged on ctx-cancel open error; got:\n%s", buf.String())
	}
}

func TestSupervisor_CtxCancelledDuringStream_NoWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger}

	readEntered := make(chan struct{})

	fake := &fakeOpener{
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
		s.streamFrom(ctx, "cid", fake, s.logger, 0)
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

func TestSupervisor_ContainerLogsOptions(t *testing.T) {
	s := &Supervisor{logger: slog.Default()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var capturedID string
	var capturedOpts dockerclient.ContainerLogsOptions

	fake := &fakeOpener{
		logsFn: func(ctx context.Context, containerID string, opts dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			capturedID = containerID
			capturedOpts = opts
			return nil, ctx.Err()
		},
	}

	s.streamFrom(ctx, "test-container-id", fake, s.logger, 0)

	if capturedID != "test-container-id" {
		t.Errorf("containerID not forwarded; want %q, got %q", "test-container-id", capturedID)
	}
	if !capturedOpts.Follow {
		t.Error("ContainerLogs must be called with Follow: true")
	}
	if !capturedOpts.ShowStdout {
		t.Error("ContainerLogs must be called with ShowStdout: true")
	}
	if !capturedOpts.ShowStderr {
		t.Error("ContainerLogs must be called with ShowStderr: true")
	}
}

// ─── Supervisor.Wait deadline ──────────────────────────────────────────────────

func TestSupervisor_Wait_DeadlineExceeded_LogsWarning(t *testing.T) {
	logger, buf := newCapturingLogger()
	// White-box: set logDone directly to test Wait's timeout path in isolation.
	s := &Supervisor{
		logger:            logger,
		effectiveStopWait: 20 * time.Millisecond,
	}
	logDone := make(chan struct{})
	s.logDone = logDone
	t.Cleanup(func() { close(logDone) }) // unblock the watchdog goroutine

	s.Wait()

	// On timeout, the goroutine may still be alive — Running() must stay true
	// to prevent Start from spawning a second goroutine (zombie).
	if !s.Running() {
		t.Error("Wait must NOT clear logDone on deadline; the goroutine may still be live")
	}
	out := buf.String()
	if !strings.Contains(out, "log stream did not stop within deadline") {
		t.Errorf("expected deadline warning; got:\n%s", out)
	}
	// The warning must surface the operational consequence — a goroutine may
	// be leaked for the remainder of the process lifetime — so an operator
	// reading the log understands the implication without code-diving.
	if !strings.Contains(out, "goroutine may leak") {
		t.Errorf("expected deadline warning to mention goroutine leak risk; got:\n%s", out)
	}
}

// TestSupervisor_Wait_DeadlineThenGoroutineExits_ClearsLogDone verifies the
// watchdog: once the goroutine eventually exits (even after Wait returned on
// timeout), Running() must flip to false so the Supervisor is reusable.
func TestSupervisor_Wait_DeadlineThenGoroutineExits_ClearsLogDone(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		logger:            logger,
		effectiveStopWait: 20 * time.Millisecond,
	}
	logDone := make(chan struct{})
	s.logDone = logDone

	s.Wait() // returns after effectiveStopWait, watchdog now waiting on logDone

	if !s.Running() {
		t.Fatal("precondition: Running() must be true immediately after Wait timeout")
	}
	close(logDone) // simulate the goroutine finally exiting

	if !eventually(time.Second, func() bool { return !s.Running() }) {
		t.Error("Running() must become false after goroutine exits, even post-timeout")
	}
}

// TestSupervisor_Start_NoOpAfterWaitTimeout verifies that Start refuses to
// spawn a second goroutine while a prior goroutine from a timed-out Wait may
// still be alive.
func TestSupervisor_Start_NoOpAfterWaitTimeout(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		logger:            logger,
		effectiveStopWait: 20 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) {
			t.Fatal("NewOpenerFn must not be called: Start should be a no-op")
			return nil, nil, nil
		},
	}
	logDone := make(chan struct{})
	s.logDone = logDone
	t.Cleanup(func() { close(logDone) })

	s.Wait() // times out, logDone left set

	s.Start("cid-2", logger) // must be a no-op

	// If Start incorrectly spawned a goroutine, Cancel+Wait will execute it
	// through to NewOpenerFn, which calls t.Fatal. No time.Sleep needed.
	s.Cancel()
	s.Wait()
}

// ─── Supervisor.stream (Docker client wiring) ──────────────────────────────────

func TestSupervisor_DockerClientCreateFailure_LogsWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{
		logger:      logger,
		StopWait:    100 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) { return nil, nil, errors.New("no docker socket") },
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stream(context.Background(), "cid", s.logger, 0, s.NewOpenerFn)
	}()

	awaitDone(t, done, time.Second)

	if !strings.Contains(buf.String(), "failed to create Docker client") {
		t.Errorf("expected warn about client failure; got:\n%s", buf.String())
	}
}

func TestSupervisor_DockerClientCreateFailure_CtxCancelled_NoWarn(t *testing.T) {
	logger, buf := newCapturingLogger()
	s := &Supervisor{
		logger:      logger,
		NewOpenerFn: func() (opener, func(), error) { return nil, nil, errors.New("no docker socket") },
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stream(ctx, "cid", logger, 0, s.NewOpenerFn)
	}()

	awaitDone(t, done, time.Second)

	if strings.Contains(buf.String(), "failed to create Docker client") {
		t.Errorf("expected warn to be suppressed when ctx was cancelled; got:\n%s", buf.String())
	}
}

// ─── Supervisor.Cancel / Wait no-ops ──────────────────────────────────────────

func TestSupervisor_CancelBeforeStart_IsNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Cancel() on zero-value Supervisor panicked: %v", r)
		}
	}()
	var s Supervisor
	s.Cancel()
}

func TestSupervisor_WaitBeforeStart_IsNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Wait() on zero-value Supervisor panicked: %v", r)
		}
	}()
	var s Supervisor
	s.Wait()
}

// ─── Supervisor.Running ────────────────────────────────────────────────────────

func TestSupervisor_Running_FalseWhenNotStarted(t *testing.T) {
	var s Supervisor
	if s.Running() {
		t.Error("zero-value Supervisor should not be running")
	}
}

func TestSupervisor_Running_TrueWhenLogDoneSet(t *testing.T) {
	// White-box: set logDone directly to test Running() in isolation from Start().
	s := Supervisor{}
	s.logDone = make(chan struct{})
	if !s.Running() {
		t.Error("Running() should be true when logDone is non-nil")
	}
}

func TestSupervisor_Running_FalseAfterCancelAndWait(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		logger:      logger,
		StopWait:    100 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) { return nil, nil, errors.New("test") },
	}

	s.Start("cid", logger)
	s.Cancel()
	s.Wait()

	if s.Running() {
		t.Error("Running() should be false after Cancel + Wait")
	}
}

// TestSupervisor_clearCycleState_StaleChanIsNoop guards against a watchdog
// from a previous Wait timeout wiping per-cycle state that belongs to a
// fresh Start cycle. Only the cycle whose logDone matches s.logDone may clear.
func TestSupervisor_clearCycleState_StaleChanIsNoop(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{}

	// Simulate state from a fresh cycle currently in flight.
	freshDone := make(chan struct{})
	s.logDone = freshDone
	s.logger = logger
	s.effectiveStopWait = 7 * time.Second

	// A stale watchdog fires for an older logDone — must be a no-op.
	staleDone := make(chan struct{})
	close(staleDone)
	s.clearCycleState(staleDone)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logDone != freshDone {
		t.Error("stale watchdog must not clear s.logDone of a fresh cycle")
	}
	if s.logger != logger {
		t.Error("stale watchdog must not clear s.logger of a fresh cycle")
	}
	if s.effectiveStopWait != 7*time.Second {
		t.Errorf("stale watchdog must not clear s.effectiveStopWait; got %v", s.effectiveStopWait)
	}
}

func TestSupervisor_Wait_ClearsLoggerFieldAfterDrain(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		StopWait:    100 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) { return nil, nil, errors.New("stop") },
	}

	s.Start("cid", logger)
	s.Cancel()
	s.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logger != nil {
		t.Error("Wait should clear s.logger after the goroutine drains; it persists between cycles otherwise")
	}
	if s.effectiveStopWait != 0 {
		t.Errorf("Wait should clear s.effectiveStopWait after drain; got %v", s.effectiveStopWait)
	}
}

// ─── Start guard and default-application paths ────────────────────────────────

func TestSupervisor_Start_NoopWhenAlreadyRunning(t *testing.T) {
	logger, _ := newCapturingLogger()

	entered := make(chan struct{})
	s := &Supervisor{
		StopWait: 100 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) {
			fake := &fakeOpener{
				logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			return fake, func() {}, nil
		},
	}

	s.Start("cid", logger)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("goroutine never started")
	}

	s.mu.Lock()
	firstDone := s.logDone
	s.mu.Unlock()

	s.Start("cid", logger) // must be no-op

	s.mu.Lock()
	secondDone := s.logDone
	s.mu.Unlock()

	if secondDone != firstDone {
		t.Error("second Start must not replace logDone — supervisor was already running")
	}

	s.Cancel()
	s.Wait()
}

func TestSupervisor_Start_AppliesStopWaitDefault(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		// StopWait intentionally left zero
		NewOpenerFn: func() (opener, func(), error) { return nil, nil, errors.New("stop") },
	}

	s.Start("cid", logger)

	// Observe Start's resolution *before* Wait, which is responsible for
	// clearing per-cycle state once the goroutine drains.
	s.mu.Lock()
	eff := s.effectiveStopWait
	exported := s.StopWait
	s.mu.Unlock()

	s.Cancel()
	s.Wait()

	if eff != 5*time.Second {
		t.Errorf("Start should resolve effective StopWait to 5s when zero; got %v", eff)
	}
	if exported != 0 {
		t.Errorf("Start must not mutate exported StopWait field; got %v", exported)
	}
}

// ─── stream success path ──────────────────────────────────────────────────────

func TestSupervisor_stream_SuccessPath_CloserCalled(t *testing.T) {
	logger, _ := newCapturingLogger()

	closerCalled := false
	entered := make(chan struct{}, 1)

	s := &Supervisor{
		logger: logger,
		NewOpenerFn: func() (opener, func(), error) {
			fake := &fakeOpener{
				logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
					entered <- struct{}{}
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			return fake, func() { closerCalled = true }, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stream(ctx, "cid", s.logger, 0, s.NewOpenerFn)
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("streamFrom was never called")
	}
	cancel()
	awaitDone(t, done, time.Second)

	if !closerCalled {
		t.Error("stream must call the cleanup func when opener succeeds")
	}
}

// ─── newDockerOpener and nil-fallback path ────────────────────────────────────

// TestNewDockerOpener_CreatesOpener verifies that newDockerOpener successfully
// constructs a Docker client opener and cleanup func. dockerclient.New does
// not require a running daemon — it only reads env vars and constructs the
// client struct — so this test is reliable without Docker.
func TestNewDockerOpener_CreatesOpener(t *testing.T) {
	t.Parallel() // newDockerOpener is a top-level func; safe under parallel.
	o, cleanup, err := newDockerOpener()
	if err != nil {
		t.Skipf("Docker client creation failed (unusual env): %v", err)
	}
	if o == nil {
		t.Error("newDockerOpener must return a non-nil opener on success")
	}
	if cleanup == nil {
		t.Error("newDockerOpener must return a non-nil cleanup func on success")
	}
	cleanup()
}

// TestSupervisor_stream_NilOpenerFn_InvokesRealDockerOpener verifies that the
// nil-NewOpenerFn branch in stream() actually routes through newDockerOpener
// (and not, say, a stub that returns early). The behavioral signal is that
// stream emits one of the two log lines that can only originate from code
// reached *after* the nil-branch resolves makeOpener to newDockerOpener:
//
//   - "failed to create Docker client for log streaming" — newDockerOpener
//     returned an error (e.g., DOCKER_HOST malformed).
//   - "log stream failed to open" — newDockerOpener succeeded and the
//     resulting opener's ContainerLogs call failed (no daemon → connection
//     error; daemon reachable → 404 for the nonexistent container ID).
//
// Either log proves the real opener path executed. A broken nil-branch (one
// that returned without calling makeOpener, or wired a nil opener) would
// produce neither.
//
// Parallel-safe: no package-level state is mutated. ctx is left live so the
// ctx-cancel suppression branches are not taken — the log must surface.
func TestSupervisor_stream_NilOpenerFn_InvokesRealDockerOpener(t *testing.T) {
	t.Parallel()
	logger, buf := newCapturingLogger()
	s := &Supervisor{logger: logger} // NewOpenerFn intentionally nil

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.stream(t.Context(), "cid", s.logger, 0, nil)
	}()
	awaitDone(t, done, 3*time.Second)

	out := buf.String()
	if !strings.Contains(out, "log stream failed to open") &&
		!strings.Contains(out, "failed to create Docker client for log streaming") {
		t.Errorf("expected real-opener path to log an open/create failure; got:\n%s", out)
	}
}

// ─── lineWriter io.Writer contract ────────────────────────────────────────────

// TestLineWriter_NewlineOnlyFrameReturnsLen verifies that a frame containing
// only whitespace (e.g. "\n") is suppressed from slog but still reports the
// original byte count, satisfying the io.Writer contract (n must equal len(p)).
func TestLineWriter_NewlineOnlyFrameReturnsLen(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}
	input := []byte("\n")
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write must return len(p)=%d on suppressed frame; got %d", len(input), n)
	}
	if buf.Len() != 0 {
		t.Errorf("suppressed frame must produce no log entries; got:\n%s", buf.String())
	}
}

// ─── streamFrom backoff cancellation ─────────────────────────────────────────

// TestSupervisor_CtxCancelledDuringBackoff verifies that cancelling the context
// during the restart backoff (after a clean EOF) causes the supervisor to exit
// promptly rather than waiting out the full RestartBackoff duration.
func TestSupervisor_CtxCancelledDuringBackoff(t *testing.T) {
	logger, _ := newCapturingLogger()
	// Use a long backoff so the test would time out if ctx cancellation is ignored.
	s := &Supervisor{logger: logger, RestartBackoff: 10 * time.Second}

	var (
		callsMu sync.Mutex
		calls   int
	)

	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			callsMu.Lock()
			calls++
			n := calls
			callsMu.Unlock()
			if n == 1 {
				// Return clean EOF to trigger the restart backoff.
				return makeMultiplexedStream("", ""), nil
			}
			// Should not be reached — ctx should be cancelled during backoff.
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.streamFrom(ctx, "cid", fake, s.logger, s.RestartBackoff)
	}()

	// Wait until the first ContainerLogs call has returned (clean EOF), meaning
	// the supervisor is now in the backoff select. Then cancel — the supervisor
	// must exit promptly without making a second call.
	if !eventually(2*time.Second, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return calls >= 1
	}) {
		t.Fatal("first ContainerLogs call never happened")
	}

	cancel()
	awaitDone(t, done, 2*time.Second)

	callsMu.Lock()
	finalCalls := calls
	callsMu.Unlock()
	if finalCalls > 1 {
		t.Errorf("supervisor should have exited during backoff without making a second ContainerLogs call; got %d calls", finalCalls)
	}
}

// ─── counter-helper pattern in fakeOpener must be lock-safe ─────────────────

// TestCallCounterPattern_LockedReadRequired guards the counter pattern used by
// fakeOpener closures (lock → increment → unlock → branch on counter). The
// branch read MUST snapshot the counter under the lock; reading the bare field
// after Unlock races against concurrent invocations.
//
// firstCallBranch encapsulates the pattern under test so the production helper
// in fakeOpener closures stays in sync with the contract verified here.
func TestCallCounterPattern_LockedReadRequired(t *testing.T) {
	var callsMu sync.Mutex
	calls := 0
	var branches int64

	step := func() {
		if firstCallBranch(&callsMu, &calls) {
			atomic.AddInt64(&branches, 1)
		}
	}

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(step)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&branches); got != 1 {
		t.Errorf("first-call branch must fire exactly once across concurrent invocations; got %d", got)
	}
}

// ─── Start must not mutate caller-owned exported fields ──────────────────────

// TestSupervisor_Start_DoesNotMutateExportedFields asserts that Start applies
// defaults internally without writing back to the exported RestartBackoff /
// StopWait fields. Callers own those fields and may inspect them after Start;
// the supervisor must not silently overwrite their zero values.
func TestSupervisor_Start_DoesNotMutateExportedFields(t *testing.T) {
	logger, _ := newCapturingLogger()

	s := &Supervisor{
		// RestartBackoff and StopWait intentionally left zero
		NewOpenerFn: func() (opener, func(), error) {
			return nil, nil, errors.New("stop")
		},
	}

	s.Start("cid", logger)
	s.Cancel()
	s.Wait()

	if s.RestartBackoff != 0 {
		t.Errorf("Start must not mutate RestartBackoff; got %v", s.RestartBackoff)
	}
	if s.StopWait != 0 {
		t.Errorf("Start must not mutate StopWait; got %v", s.StopWait)
	}
}

// ─── goroutine isolation from supervisor field mutation ──────────────────────

// TestSupervisor_ExternalMutationOfRestartBackoff_NoRace verifies that the
// streaming goroutine does not read s.RestartBackoff after Start. Even if a
// caller violates the documented contract ("set before Start") and mutates the
// field concurrently, the race detector must not fire.
//
// Before the fix: streamFrom reads s.RestartBackoff in its restart-backoff
// select, racing with the test's writes. After the fix: the goroutine reads
// only a snapshot captured under s.mu inside Start.
func TestSupervisor_ExternalMutationOfRestartBackoff_NoRace(t *testing.T) {
	logger, _ := newCapturingLogger()

	// Use a fake that returns clean EOF every time so the goroutine constantly
	// cycles through the backoff select that reads RestartBackoff.
	fake := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			return makeMultiplexedStream("", ""), nil
		},
	}

	s := &Supervisor{
		RestartBackoff: 1 * time.Millisecond,
		StopWait:       2 * time.Second,
		NewOpenerFn:    func() (opener, func(), error) { return fake, func() {}, nil },
	}

	s.Start("cid", logger)

	// Hammer the field from the main goroutine while the streaming goroutine
	// is actively cycling through its backoff loop. Without a snapshot, the
	// race detector will fire on the goroutine's unsynchronized read.
	mutateDone := make(chan struct{})
	go func() {
		defer close(mutateDone)
		for i := range 1000 {
			s.RestartBackoff = time.Duration(i%5+1) * time.Millisecond
		}
	}()

	<-mutateDone
	s.Cancel()
	s.Wait()
}

func TestSupervisor_ContextCancelledOnExit(t *testing.T) {
	var capturedCtx context.Context
	o := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			capturedCtx = ctx
			return nil, errors.New("open error")
		},
	}
	s := &Supervisor{
		RestartBackoff: 10 * time.Millisecond,
		NewOpenerFn:    func() (opener, func(), error) { return o, func() {}, nil },
	}
	s.Start("test-container", slog.Default())
	s.Wait()

	if capturedCtx == nil {
		t.Fatal("expected logsFn to be called and capture context")
	}
	select {
	case <-capturedCtx.Done():
		// Pass
	default:
		t.Error("expected context to be cancelled/done after supervisor Wait")
	}

	s.mu.Lock()
	logStop := s.logStop
	s.mu.Unlock()
	if logStop != nil {
		t.Error("expected s.logStop to be nil after supervisor exits/Wait")
	}
}

type testLogHandler struct {
	emit func(slog.Record)
}

func (h *testLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *testLogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.emit(record)
	return nil
}

func (h *testLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *testLogHandler) WithGroup(name string) slog.Handler {
	return h
}

func encodeFrame(streamType byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, []byte(payload)...)
}

func TestSupervisor_PartialLinesPersistAcrossRestarts(t *testing.T) {
	var logs []string
	var mu sync.Mutex
	handler := &testLogHandler{
		emit: func(record slog.Record) {
			mu.Lock()
			logs = append(logs, record.Message)
			mu.Unlock()
		},
	}
	testLogger := slog.New(handler)

	var (
		callCountMu sync.Mutex
		callCount   int
	)
	o := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			callCountMu.Lock()
			callCount++
			n := callCount
			callCountMu.Unlock()
			if n == 1 {
				// Standard header + "start of line" without newline
				return io.NopCloser(bytes.NewReader(encodeFrame(1, "start of line"))), nil
			}
			if n == 2 {
				// Standard header + " and end\n"
				return io.NopCloser(bytes.NewReader(encodeFrame(1, " and end\n"))), nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	s := &Supervisor{
		RestartBackoff: 1 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) {
			return o, func() {}, nil
		},
	}

	s.Start("test-container", testLogger)
	if !eventually(500*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(logs) > 0
	}) {
		t.Fatal("timed out waiting for log line")
	}
	s.Cancel()
	s.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || logs[0] != "start of line and end" {
		t.Errorf("expected combined log 'start of line and end', got: %v", logs)
	}
}

func TestSupervisor_StartSucceedsImmediatelyPostExit(t *testing.T) {
	s := &Supervisor{
		RestartBackoff: 10 * time.Millisecond,
	}
	o := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			return io.NopCloser(bytes.NewReader(nil)), nil
		},
	}
	s.NewOpenerFn = func() (opener, func(), error) { return o, func() {}, nil }

	logger1 := slog.Default()
	logger2 := slog.New(slog.Default().Handler())

	s.Start("container-1", logger1)
	// Cancel and wait for exit, but call Start immediately to trigger the race.
	s.Cancel()
	// Sleep briefly so the stream goroutine actually exits (its logDone is closed),
	// but state is not cleared because Wait() wasn't called.
	time.Sleep(10 * time.Millisecond)

	// Start again. This should succeed.
	s.Start("container-2", logger2)

	s.mu.Lock()
	logger := s.logger
	s.mu.Unlock()

	s.Cancel()
	s.Wait()
	if logger != logger2 {
		t.Fatal("expected second Start to succeed and register logger")
	}
}
// ─── lineWriter allocations ────────────────────────────────────────────────────

func TestLineWriter_AllocationsPerLine(t *testing.T) {
	handler := &testLogHandler{emit: func(record slog.Record) {}}
	logger := slog.New(handler)
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = w.Write([]byte("hello world\n"))
	})
	// Slicing rest and casting to string should only trigger 1 allocation (the string creation itself)
	if allocs > 1 {
		t.Errorf("expected at most 1 allocation per line, got: %f", allocs)
	}
}

// ─── byteCounter ──────────────────────────────────────────────────────────────

func TestByteCounter_CountIsUnsigned(t *testing.T) {
	// byteCounter.count must be uint64: byte counts are never negative, and
	// using an unsigned type makes that invariant self-documenting.
	var c byteCounter
	_ = []uint64{c.count} // compile-time type assertion
}

// ─── exponential backoff ──────────────────────────────────────────────────────

// TestSupervisor_ExponentialBackoffOnFastEmptyEOFs verifies that consecutive
// fast empty EOFs cause the backoff duration to grow, and that a stream that
// delivers data resets it. Instead of measuring wall-clock intervals (which
// flake under CI load), the test gates each ContainerLogs call via channels
// and asserts on the number of calls that occur within carefully bounded
// windows.
func TestSupervisor_ExponentialBackoffOnFastEmptyEOFs(t *testing.T) {
	const baseBackoff = 50 * time.Millisecond

	// callCh receives a signal every time ContainerLogs is invoked.
	callCh := make(chan struct{}, 20)

	o := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			select {
			case callCh <- struct{}{}:
			default:
			}
			// Return immediately with no data → fast empty EOF.
			return io.NopCloser(bytes.NewReader(nil)), nil
		},
	}

	s := &Supervisor{
		RestartBackoff: baseBackoff,
	}
	s.NewOpenerFn = func() (opener, func(), error) { return o, func() {}, nil }
	s.Start("test-container", slog.Default())

	// Drain calls and record how many arrive per window. With base=50ms and
	// exponential doubling, the first 4 expected backoffs are 50, 100, 200,
	// 400ms. We observe over a window that is long enough for multiple early
	// calls but shorter than a single late backoff, proving growth.
	//
	// Strategy: count how many calls arrive in successive 150ms windows.
	// Window 1 (0–150ms):   should see ≥2 calls (initial + after 50ms backoff)
	// Window 2 (150–300ms): should see ≤1 call  (100ms or 200ms backoff)
	// If backoff were constant at 50ms, window 2 would see ~3 calls.

	countCallsInWindow := func(d time.Duration) int {
		timer := time.NewTimer(d)
		defer timer.Stop()
		n := 0
		for {
			select {
			case <-callCh:
				n++
			case <-timer.C:
				return n
			}
		}
	}

	window1 := countCallsInWindow(150 * time.Millisecond)
	window2 := countCallsInWindow(150 * time.Millisecond)

	s.Cancel()
	s.Wait()

	// Window 1 must have seen at least 2 calls (the initial call is near-
	// instant, then the 50ms backoff fires within the 150ms window).
	if window1 < 2 {
		t.Errorf("window 1: expected ≥2 calls, got %d", window1)
	}
	// Window 2 must have fewer calls than window 1 — the growing backoff
	// means calls arrive less frequently.
	if window2 >= window1 {
		t.Errorf("expected fewer calls in window 2 (backoff should grow): window1=%d window2=%d", window1, window2)
	}
}

func TestSupervisor_StartCancelsPreviousContext(t *testing.T) {
	logger, _ := newCapturingLogger()
	s := &Supervisor{logger: logger}

	var (
		mu          sync.Mutex
		capturedCtx context.Context
	)

	runDone := make(chan struct{})
	var closeOnce sync.Once

	o := &fakeOpener{
		logsFn: func(ctx context.Context, _ string, _ dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
			mu.Lock()
			capturedCtx = ctx
			mu.Unlock()
			closeOnce.Do(func() {
				close(runDone)
			})
			return nil, errors.New("exit immediately with error")
		},
	}
	s.NewOpenerFn = func() (opener, func(), error) { return o, func() {}, nil }

	s.Start("test-container", logger)

	// Wait for the first run's goroutine to complete its stream call
	<-runDone

	// Give a tiny amount of time for the goroutine's defer block to finish and close logDone
	s.mu.Lock()
	logDone := s.logDone
	s.mu.Unlock()
	if logDone != nil {
		<-logDone
	}

	mu.Lock()
	ctxAfterFirstRun := capturedCtx
	mu.Unlock()

	if ctxAfterFirstRun == nil {
		t.Fatal("expected captured context from first run, got nil")
	}

	// Since s.Wait() was not called, clearCycleState was not run, so context should not be canceled yet.
	if err := ctxAfterFirstRun.Err(); err != nil {
		t.Fatalf("context should not be canceled yet since clearCycleState was not called; got: %v", err)
	}

	// Start again, which should trigger cleanup of the completed run and cancel its context
	s.Start("test-container-2", logger)

	if err := ctxAfterFirstRun.Err(); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected previous run's context to be canceled during startup cleanup, got: %v", err)
	}

	s.Cancel()
	s.Wait()
}

type dummyHandler struct{}

func (dummyHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (dummyHandler) Handle(context.Context, slog.Record) error { return nil }
func (d dummyHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return d }
func (d dummyHandler) WithGroup(name string) slog.Handler    { return d }

func TestLineWriter_Allocations(t *testing.T) {
	w := &lineWriter{
		ctx:        context.Background(),
		logger:     slog.New(dummyHandler{}),
		level:      slog.LevelInfo,
		maxLineLen: 1000000, // large enough to avoid chunking during alloc test
	}

	largeLine := append(make([]byte, 100000), '\n')
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = w.Write(largeLine)
	})

	t.Logf("--- ALLOCS: %f", allocs)
	if allocs > 1.0 {
		t.Errorf("writing aligned line when buffer is empty should allocate at most once; got: %f", allocs)
	}
}

func TestLineWriter_MaxLineLength(t *testing.T) {
	logger, buf := newCapturingLogger()
	w := &lineWriter{
		ctx:        context.Background(),
		logger:     logger,
		level:      slog.LevelInfo,
		maxLineLen: 10,
	}

	// Write 25 bytes without newline
	input := []byte("abcdefghijklmnopqrstuvwxy") // 25 bytes
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(input) {
		t.Errorf("expected to consume %d bytes, got %d", len(input), n)
	}

	// It should have split and logged 2 chunks of 10 bytes: "abcdefghij" and "klmnopqrst"
	// The remaining 5 bytes "uvwxy" should be in the buffer.
	out := buf.String()
	if !strings.Contains(out, "abcdefghij") {
		t.Errorf("expected first segment 'abcdefghij', got: %s", out)
	}
	if !strings.Contains(out, "klmnopqrst") {
		t.Errorf("expected second segment 'klmnopqrst', got: %s", out)
	}
	if strings.Contains(out, "uvwxy") {
		t.Errorf("remaining segment should not be logged yet, got: %s", out)
	}

	// Now write a newline to flush the remaining 5 bytes
	_, err = w.Write([]byte("\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	out2 := buf.String()
	if !strings.Contains(out2, "uvwxy") {
		t.Errorf("expected remaining segment 'uvwxy' to be logged, got: %s", out2)
	}
}

func TestSupervisor_Running_AccurateStatus(t *testing.T) {
	// A supervisor that exits on its own (e.g. open failure) should report Running() as false
	// even before Wait() has been called.
	logger, _ := newCapturingLogger()
	s := &Supervisor{
		RestartBackoff: 10 * time.Millisecond,
		StopWait:       100 * time.Millisecond,
		NewOpenerFn: func() (opener, func(), error) {
			return nil, nil, errors.New("open error")
		},
	}

	s.Start("test-container", logger)
	if !s.Running() {
		t.Error("expected Running() to be true immediately after Start")
	}

	// Wait for goroutine to exit on its own due to the open failure.
	// Since the goroutine exits, s.Running() should become false, even though Wait() hasn't run yet.
	if !eventually(500*time.Millisecond, func() bool { return !s.Running() }) {
		t.Error("expected Running() to become false after goroutine exits")
	}

	s.Wait()
	if s.Running() {
		t.Error("expected Running() to remain false after Wait")
	}
}

// ─── nil-context regression ────────────────────────────────────────────────────

// strictCtxHandler is an slog.Handler that fails the test if Handle receives
// a nil context. This validates that lineWriter always passes a non-nil ctx.
type strictCtxHandler struct {
	t      *testing.T
	inner  slog.Handler
	called atomic.Bool
}

func (h *strictCtxHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *strictCtxHandler) Handle(ctx context.Context, record slog.Record) error {
	h.called.Store(true)
	if ctx == nil {
		h.t.Error("lineWriter passed nil context to slog.Handler.Handle — ctx field is missing")
	}
	return h.inner.Handle(ctx, record)
}

func (h *strictCtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &strictCtxHandler{t: h.t, inner: h.inner.WithAttrs(attrs)}
}

func (h *strictCtxHandler) WithGroup(name string) slog.Handler {
	return &strictCtxHandler{t: h.t, inner: h.inner.WithGroup(name)}
}

// TestLineWriter_ContextNotNil is a regression guard: lineWriter must always
// be constructed with a non-nil ctx so the context reaches slog.Handler.Handle.
// The stdlib currently substitutes context.Background() for nil inside
// slog.Logger.Log, but that is an undocumented implementation detail.
func TestLineWriter_ContextNotNil(t *testing.T) {
	buf := &syncBuffer{}
	handler := &strictCtxHandler{
		t:     t,
		inner: slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	}
	logger := slog.New(handler)
	w := &lineWriter{ctx: context.Background(), logger: logger, level: slog.LevelInfo}

	if w.ctx == nil {
		t.Fatal("lineWriter.ctx must not be nil")
	}

	_, _ = w.Write([]byte("hello\n"))

	if !handler.called.Load() {
		t.Fatal("handler was never called — test is not exercising the code path")
	}
}

