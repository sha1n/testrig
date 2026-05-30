package wiremock_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/testrig/api"
	"github.com/sha1n/testrig/services/wiremock"
)

// concurrentBuffer is a goroutine-safe sink for slog. The testcontainers log
// consumer invokes our adapter from a background goroutine, so the raw
// bytes.Buffer used by slog.TextHandler must be guarded.
type concurrentBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *concurrentBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *concurrentBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newCapturingHandle(t *testing.T) (api.EnvHandle, *concurrentBuffer) {
	t.Helper()
	buf := &concurrentBuffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(h)
	return api.StubEnvHandle("test", logger, nil), buf
}

// waitForLog polls the buffer until sentinel appears or timeout elapses.
// The Eventually pattern: succeed the moment the condition holds, fail
// only after a generous timeout — never on a fixed sleep that might be
// too short on a busy CI host.
func waitForLog(buf *concurrentBuffer, sentinel string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), sentinel) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// assertBufferStaysAt asserts the buffer never grows past snapshot during
// the window. Inverse of waitForLog: fails the moment new bytes arrive
// (not at the window's end), avoiding fixed-sleep flakiness while still
// catching violations.
func assertBufferStaysAt(t *testing.T, buf *concurrentBuffer, snapshot string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if got := buf.String(); got != snapshot {
			t.Errorf("buffer grew during stay-window:\n%s", strings.TrimPrefix(got, snapshot))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWireMock_Defaults(t *testing.T) {
	tk := wiremock.New("test-mock")

	if tk.Name() != "test-mock" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	url, ok := props["test-mock.url"]
	if !ok || !strings.Contains(url, "http://localhost:") {
		t.Errorf("Expected URL property to start with http://localhost:, got %s", url)
	}
}

func TestWireMock_Configured(t *testing.T) {
	tk := wiremock.New("custom-mock").
		WithImage("wiremock/wiremock").
		WithTag("3.3.1")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if url := props["custom-mock.url"]; url == "" {
		t.Error("Expected url property to be populated")
	}
}

func TestWireMock_URLPropertyName_Override(t *testing.T) {
	tk := wiremock.New("wm").WithURLPropertyName("MOCK_URL")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if props["MOCK_URL"] == "" {
		t.Error("MOCK_URL property not published under custom key")
	}
	if _, ok := props["wm.url"]; ok {
		t.Error("default key wm.url should not be published when overridden")
	}
}

func TestWireMock_StartTwice_ReturnsError(t *testing.T) {
	tk := wiremock.New("twice")
	if _, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if _, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err == nil {
		t.Error("Expected error on second Start")
	}
}

func TestWireMock_StopThenStart_Succeeds(t *testing.T) {
	// A service instance must be reusable across env restart cycles. Stop
	// releases the container and clears service state so a subsequent Start
	// builds a fresh one.
	tk := wiremock.New("restart-test")
	ctx := context.Background()

	if _, err := tk.Start(ctx, api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if _, err := tk.Start(ctx, api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("second Start after Stop must succeed; got: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestWireMock_Stop_NoContainer(t *testing.T) {
	tk := wiremock.New("no-container")
	if err := tk.Stop(context.Background()); err != nil {
		t.Errorf("Stop without container should be no-op, got %v", err)
	}
}

func TestWireMock_URL_MatchesProperty(t *testing.T) {
	tk := wiremock.New("url-match")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.URL() != props["url-match.url"] {
		t.Errorf("URL() and url-match.url property should match. URL()=%s prop=%s", tk.URL(), props["url-match.url"])
	}
}

// verboseSentinel is a substring uniquely produced by WireMock's --verbose
// startup options dump (see the option-table lines like "no-request-journal:
// false" that appear on the container's stdout once verbose is on).
const verboseSentinel = "no-request-journal"

func TestWireMock_DefaultStart_DoesNotForwardContainerOutput(t *testing.T) {
	// Without WithVerboseLogging, no LogConsumer is wired into the
	// ContainerRequest, so no log producer ever starts — there's no async
	// path through which container stdout could reach the testrig logger.
	// Asserting absence is safe immediately after Start returns.
	tk := wiremock.New("verbose-off")
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(context.Background(), handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if strings.Contains(buf.String(), verboseSentinel) {
		t.Errorf("container verbose output leaked into logger without WithVerboseLogging; buf:\n%s", buf.String())
	}
}

func TestWireMock_VerboseLogging_ForwardsContainerOutput(t *testing.T) {
	tk := wiremock.New("verbose-on").WithVerboseLogging()
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(context.Background(), handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if !waitForLog(buf, verboseSentinel, 10*time.Second) {
		t.Errorf("expected verbose sentinel %q in logger output; buf:\n%s", verboseSentinel, buf.String())
	}
}

// bannerSentinel is the URL line printed inside WireMock's ASCII art startup
// banner. Distinct from the verbose options dump, so its presence/absence
// reliably distinguishes "banner shown" from "banner suppressed".
const bannerSentinel = "wiremock.org"

func TestWireMock_VerboseLogging_SuppressesBannerByDefault(t *testing.T) {
	tk := wiremock.New("banner-off").WithVerboseLogging()
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(context.Background(), handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	// WireMock prints the banner BEFORE the verbose options dump, so the
	// options-table sentinel arriving guarantees that any banner line —
	// were it going to appear — has already been delivered to the
	// consumer. No extra grace period needed.
	if !waitForLog(buf, verboseSentinel, 10*time.Second) {
		t.Fatalf("verbose output never arrived; cannot assert banner absence. buf:\n%s", buf.String())
	}

	if strings.Contains(buf.String(), bannerSentinel) {
		t.Errorf("banner sentinel %q present despite default-disabled banner; buf:\n%s", bannerSentinel, buf.String())
	}
}

// TestWireMock_VerboseLogging_SurvivesStartCtxCancel regression-pins the fix
// for a bug in which post-startup container output was silently dropped.
// testrig's env.runTrack runs services under errgroup.WithContext, whose ctx
// is cancelled the moment all services in a stage finish starting. The
// supervisor must own its own (background-derived) ctx so it can keep
// streaming after the caller's Start ctx is cancelled.
func TestWireMock_VerboseLogging_SurvivesStartCtxCancel(t *testing.T) {
	startCtx, cancelStart := context.WithCancel(context.Background())
	tk := wiremock.New("ctx-cancel").WithVerboseLogging()
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(startCtx, handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if !waitForLog(buf, verboseSentinel, 10*time.Second) {
		t.Fatalf("startup output never arrived. buf:\n%s", buf.String())
	}

	// Mimic errgroup's cleanup-cancel of the per-stage ctx, then verify a
	// post-cancel request still produces per-request logs.
	cancelStart()

	startupSnapshot := buf.String()
	resp, err := http.Get(tk.URL() + "/probe-after-cancel")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	_ = resp.Body.Close()

	if !waitForLog(buf, "Request received", 10*time.Second) {
		newOutput := strings.TrimPrefix(buf.String(), startupSnapshot)
		t.Errorf("per-request output never arrived after start-ctx cancel. New output:\n%s", newOutput)
	}
}

// TestWireMock_VerboseLogging_SustainedMultipleRequests verifies that per-request
// logs continue to arrive across multiple log-production timeout cycles
// (default 5 s each). A bug in which the producer exits on clean EOF from
// Docker Desktop would cause later requests to produce no output, failing the
// final assertions.
func TestWireMock_VerboseLogging_SustainedMultipleRequests(t *testing.T) {
	tk := wiremock.New("sustained").WithVerboseLogging()
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(context.Background(), handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if !waitForLog(buf, verboseSentinel, 10*time.Second) {
		t.Fatalf("startup output never arrived; buf:\n%s", buf.String())
	}

	const (
		requests = 4
		interval = 4 * time.Second // spans >3 default 5-second production cycles
	)

	for i := range requests {
		// Snapshot the buffer just before the request so we can detect new
		// output that is specifically attributable to this request.
		snapshot := buf.String()

		resp, err := http.Get(fmt.Sprintf("%s/sustained-probe-%d", tk.URL(), i))
		if err != nil {
			t.Fatalf("request %d: GET failed: %v", i, err)
		}
		_ = resp.Body.Close()

		if !waitForLog(buf, "Request received", 10*time.Second) {
			newOutput := strings.TrimPrefix(buf.String(), snapshot)
			t.Errorf("request %d: per-request log never arrived. New output:\n%s", i, newOutput)
		}

		if i < requests-1 {
			time.Sleep(interval)
		}
	}
}

// TestWireMock_VerboseLogging_RestartsCleanly exercises the Stop→Start
// round-trip with verbose enabled. The branch added a logCancel field and
// manual FollowOutput/StartLogProducer wiring that must be cleared by Stop
// and re-established by the next Start; without that, the second producer
// would either not start or would be cancelled by stale state.
func TestWireMock_VerboseLogging_RestartsCleanly(t *testing.T) {
	tk := wiremock.New("restart-verbose").WithVerboseLogging()
	ctx := context.Background()

	handle1, buf1 := newCapturingHandle(t)
	if _, err := tk.Start(ctx, handle1); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if !waitForLog(buf1, verboseSentinel, 10*time.Second) {
		t.Fatalf("startup output never arrived on first Start; buf:\n%s", buf1.String())
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Fresh handle+buffer so sentinel matches on the second run are
	// unambiguously from the second producer.
	handle2, buf2 := newCapturingHandle(t)
	if _, err := tk.Start(ctx, handle2); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer func() { _ = tk.Stop(ctx) }()

	if !waitForLog(buf2, verboseSentinel, 10*time.Second) {
		t.Fatalf("startup output never arrived on second Start; buf:\n%s", buf2.String())
	}

	resp, err := http.Get(tk.URL() + "/restart-probe")
	if err != nil {
		t.Fatalf("GET after restart: %v", err)
	}
	_ = resp.Body.Close()

	if !waitForLog(buf2, "Request received", 10*time.Second) {
		t.Errorf("post-restart per-request log missing; buf:\n%s", buf2.String())
	}
}

// TestWireMock_VerboseLogging_StopHaltsStreaming pins that Stop actually
// terminates the log producer goroutine — no late log lines should land in
// the buffer after Stop returns. Catches goroutine leaks and races where
// the producer keeps forwarding for a few iterations past Stop.
func TestWireMock_VerboseLogging_StopHaltsStreaming(t *testing.T) {
	tk := wiremock.New("stop-halts").WithVerboseLogging()
	handle, buf := newCapturingHandle(t)
	ctx := context.Background()

	if _, err := tk.Start(ctx, handle); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitForLog(buf, verboseSentinel, 10*time.Second) {
		t.Fatalf("startup output never arrived; buf:\n%s", buf.String())
	}

	// One live request first — proves the producer is actually streaming
	// before we Stop, so the post-Stop assertion is meaningful.
	resp, err := http.Get(tk.URL() + "/pre-stop")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if !waitForLog(buf, "Request received", 10*time.Second) {
		t.Fatalf("pre-stop request log never arrived; buf:\n%s", buf.String())
	}

	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Eventually-not: poll the buffer for a window; fail the moment any
	// new bytes arrive rather than waiting out a fixed sleep.
	assertBufferStaysAt(t, buf, buf.String(), 1*time.Second)
}

func TestWireMock_VerboseLogging_WithBanner_ShowsBanner(t *testing.T) {
	tk := wiremock.New("banner-on").WithVerboseLogging().WithBanner()
	handle, buf := newCapturingHandle(t)

	if _, err := tk.Start(context.Background(), handle); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if !waitForLog(buf, bannerSentinel, 10*time.Second) {
		t.Errorf("expected banner sentinel %q in logger output; buf:\n%s", bannerSentinel, buf.String())
	}
}

func TestWireMock_URL_BeforeStart(t *testing.T) {
	if got := wiremock.New("url-before-start").URL(); got != "" {
		t.Errorf("URL() before Start should return empty string; got %q", got)
	}
}

func TestWireMock_Start_Error_LeavesServiceReusable(t *testing.T) {
	tk := wiremock.New("err-reuse").WithImage("non-existent-image-12345")
	if _, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err == nil {
		t.Fatal("expected Start to fail with non-existent image")
	}

	// After a failed Start the service must be in a clean state — container
	// is nil, so the "already started" guard must not fire on the next call.
	tk.WithImage("wiremock/wiremock").WithTag("3.2.0")
	if _, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start should succeed after a failed attempt; got: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()
}

func TestWireMock_Client_NotNil(t *testing.T) {
	tk := wiremock.New("client-test")

	if _, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.Client() == nil {
		t.Error("Client() returned nil")
	}
}
