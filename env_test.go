package testrig_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/testrig"
)

// --- Mocks ---

type MockService struct {
	name       string
	deps       []string
	startErr   error
	stopErr    error
	startDelay time.Duration
	properties testrig.Properties
	onStart    func()
	onStop     func()
}

func (m *MockService) Name() string           { return m.name }
func (m *MockService) Dependencies() []string { return m.deps }

func (m *MockService) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	if m.startDelay > 0 {
		select {
		case <-time.After(m.startDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.onStart != nil {
		m.onStart()
	}
	if m.startErr != nil {
		return nil, m.startErr
	}
	return m.properties, nil
}

func (m *MockService) Stop(ctx context.Context) error {
	if m.onStop != nil {
		m.onStop()
	}
	return m.stopErr
}

type MockLifecycleHook struct {
	afterStart func(ctx context.Context, envCtx testrig.EnvContext) error
	afterStop  func(ctx context.Context, envCtx testrig.EnvContext) error
}

func (m *MockLifecycleHook) AfterStart(ctx context.Context, envCtx testrig.EnvContext) error {
	if m.afterStart != nil {
		return m.afterStart(ctx, envCtx)
	}
	return nil
}

func (m *MockLifecycleHook) AfterStop(ctx context.Context, envCtx testrig.EnvContext) error {
	if m.afterStop != nil {
		return m.afterStop(ctx, envCtx)
	}
	return nil
}

// --- Tests ---

func TestEnv_Start_Success(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"k1": "v1"}}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}, properties: testrig.Properties{"k2": "v2"}}

	env := testrig.MustNew(testrig.With(s1, s2))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	props := env.Properties()
	if props["k1"] != "v1" || props["k2"] != "v2" {
		t.Errorf("Unexpected properties: %v", props)
	}
}

func TestEnv_Start_MissingDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", deps: []string{"missing-svc"}}

	env := testrig.MustNew(testrig.With(s1))

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error due to missing dependency, got nil")
	}
	if !strings.Contains(err.Error(), "depends on unknown service missing-svc") {
		t.Errorf("Expected 'depends on unknown service missing-svc', got %v", err)
	}
}

func TestEnv_Start_CircularDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", deps: []string{"svc2"}}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}}

	env := testrig.MustNew(testrig.With(s1, s2))

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error due to circular dependency, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency detected") {
		t.Errorf("Expected circular-dependency error, got %v", err)
	}
}

func TestEnv_Start_ServiceError(t *testing.T) {
	s1 := &MockService{name: "svc1", startErr: errors.New("boom")}

	env := testrig.MustNew(testrig.With(s1))

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from service start, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("Expected error containing 'boom', got %v", err)
	}
}

func TestEnv_Start_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s1 := &MockService{name: "svc-slow", startDelay: 100 * time.Millisecond}

	env := testrig.MustNew(testrig.With(s1))
	cancel()

	err := env.Start(ctx)
	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Expected context canceled, got %v", err)
	}
}

func TestEnv_Start_ContextCancellation_WaitingForDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", startDelay: 1 * time.Second}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}}

	env := testrig.MustNew(testrig.With(s1, s2))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := env.Start(ctx)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Expected context deadline exceeded, got %v", err)
	}
}

func TestEnv_StateTransitions(t *testing.T) {
	s1 := &MockService{name: "svc-state"}
	env := testrig.MustNew(testrig.With(s1))

	// 1. Double Start
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	if err := env.Start(context.Background()); err == nil {
		t.Error("Second start should have failed")
	}

	// 2. Stop
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// 3. Double Stop (Safe)
	if err := env.Stop(context.Background()); err != nil {
		t.Errorf("Second stop should be safe/no-op, got error: %v", err)
	}

	// 4. Restart on the same Env instance must succeed end-to-end.
	if err := env.Start(context.Background()); err != nil {
		t.Errorf("Restart failed: %v", err)
	}
	_ = env.Stop(context.Background())
}

func TestEnv_Stop_ServiceError(t *testing.T) {
	s1 := &MockService{name: "svc-stop-err", stopErr: errors.New("stop-fail")}

	env := testrig.MustNew(testrig.With(s1))

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())
	if err == nil {
		t.Fatal("Expected error from service stop, got nil")
	}
	if !strings.Contains(err.Error(), "stop-fail") {
		t.Errorf("Expected error containing 'stop-fail', got %v", err)
	}
}

func TestEnv_WithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	env := testrig.MustNew(testrig.WithLogger(logger))
	if env.Properties() == nil {
		t.Error("Properties should not be nil")
	}
}

func TestEnv_Start_UnknownDependency_RejectsBeforeAnyServiceStarts(t *testing.T) {
	var validStarted bool
	valid := &MockService{name: "valid", onStart: func() { validStarted = true }}
	bad := &MockService{name: "bad", deps: []string{"missing"}}

	env := testrig.MustNew(testrig.With(valid, bad))
	if err := env.Start(context.Background()); err == nil {
		t.Fatal("Expected error due to unknown dependency, got nil")
	}
	if validStarted {
		t.Error("valid service should NOT have started; configuration is invalid")
	}
}

type ContextConsumerService struct {
	MockService
	t *testing.T
}

func (s *ContextConsumerService) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	val, ok := envCtx.Get("foo")
	if !ok || val != "bar" {
		s.t.Errorf("Expected foo=bar, got %s (ok=%v)", val, ok)
	}
	props := envCtx.Properties()
	if props["foo"] != "bar" {
		s.t.Errorf("Properties() map missing foo=bar: %v", props)
	}
	return nil, nil
}

func TestEnv_ContextAccess(t *testing.T) {
	producer := &MockService{
		name:       "producer",
		properties: testrig.Properties{"foo": "bar"},
	}
	consumer := &ContextConsumerService{
		MockService: MockService{name: "consumer", deps: []string{"producer"}},
		t:           t,
	}
	env := testrig.MustNew(testrig.With(producer, consumer))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if env.Name() != "testenv" {
		t.Errorf("Unexpected env name: %s", env.Name())
	}
	_ = env.Stop(context.Background())
}

func TestEnv_Start_Rollback(t *testing.T) {
	// Two services: s1 starts successfully, s2 fails. Rollback must stop s1
	// (it acquired resources) but must NOT call Stop on s2 (it never started).
	var stopped1, stopped2 bool
	s1 := &MockService{name: "svc1", onStop: func() { stopped1 = true }}
	s2 := &MockService{name: "svc2", startErr: errors.New("boom"), onStop: func() { stopped2 = true }}

	env := testrig.MustNew(testrig.With(s1, s2))

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !stopped1 {
		t.Error("svc1 should have been stopped (rollback)")
	}
	if stopped2 {
		t.Error("svc2 should NOT have been stopped — its Start failed, so it never acquired resources")
	}
}

func TestEnv_Start_DependencyFailure_DependentSkipped(t *testing.T) {
	// A fails to start; B depends on A. B's Start must never be called (dep
	// not ready) and B's Stop must never be called (never started).
	var aStarted, bStarted, aStopped, bStopped bool
	a := &MockService{
		name:     "A",
		startErr: errors.New("a-fail"),
		onStart:  func() { aStarted = true },
		onStop:   func() { aStopped = true },
	}
	b := &MockService{
		name:    "B",
		deps:    []string{"A"},
		onStart: func() { bStarted = true },
		onStop:  func() { bStopped = true },
	}

	env := testrig.MustNew(testrig.With(a, b))
	if err := env.Start(context.Background()); err == nil {
		t.Fatal("expected error from A's failure")
	}

	if !aStarted {
		t.Error("A's Start should have been invoked")
	}
	if bStarted {
		t.Error("B's Start should NOT have been invoked (dependency A failed)")
	}
	if aStopped {
		t.Error("A's Stop should NOT have been called (its Start failed)")
	}
	if bStopped {
		t.Error("B's Stop should NOT have been called (it never started)")
	}
}

func TestEnv_Start_Rollback_JoinsRollbackErrors(t *testing.T) {
	startErr := errors.New("boom")
	stopErr := errors.New("rollback-fail")
	s1 := &MockService{name: "s1", stopErr: stopErr}
	s2 := &MockService{name: "s2", startErr: startErr}

	env := testrig.MustNew(testrig.With(s1, s2))
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !errors.Is(err, startErr) {
		t.Errorf("returned error must wrap original Start error; got %v", err)
	}
	if !errors.Is(err, stopErr) {
		t.Errorf("returned error must also wrap rollback Stop error; got %v", err)
	}
}

func TestEnv_ParallelStartStop(t *testing.T) {
	// A -> B
	// C -> B
	// All should start/stop in parallel where possible.
	var mu sync.Mutex
	startTimes := make(map[string]time.Time)
	stopTimes := make(map[string]time.Time)

	recordStart := func(name string) { mu.Lock(); startTimes[name] = time.Now(); mu.Unlock() }
	recordStop := func(name string) { mu.Lock(); stopTimes[name] = time.Now(); mu.Unlock() }

	sB := &MockService{name: "B", startDelay: 50 * time.Millisecond, onStart: func() { recordStart("B") }, onStop: func() { recordStop("B") }}
	sA := &MockService{name: "A", deps: []string{"B"}, startDelay: 50 * time.Millisecond, onStart: func() { recordStart("A") }, onStop: func() { recordStop("A") }}
	sC := &MockService{name: "C", deps: []string{"B"}, startDelay: 50 * time.Millisecond, onStart: func() { recordStart("C") }, onStop: func() { recordStop("C") }}

	env := testrig.MustNew(testrig.With(sA, sB, sC))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if startTimes["A"].Before(startTimes["B"]) || startTimes["C"].Before(startTimes["B"]) {
		t.Error("B must start before A and C")
	}
	diff := startTimes["A"].Sub(startTimes["C"])
	if diff < 0 {
		diff = -diff
	}
	if diff > 40*time.Millisecond {
		t.Errorf("A and C did not start in parallel: diff=%v", diff)
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if stopTimes["B"].Before(stopTimes["A"]) || stopTimes["B"].Before(stopTimes["C"]) {
		t.Error("A and C must stop before B")
	}
	diff = stopTimes["A"].Sub(stopTimes["C"])
	if diff < 0 {
		diff = -diff
	}
	if diff > 40*time.Millisecond {
		t.Errorf("A and C did not stop in parallel: diff=%v", diff)
	}
}

func TestEnv_WithLifecycleHook_Success(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"foo": "bar"}}

	var startCalled, stopCalled bool
	pm := &MockLifecycleHook{
		afterStart: func(ctx context.Context, envCtx testrig.EnvContext) error {
			startCalled = true
			val, _ := envCtx.Get("foo")
			if val != "bar" {
				t.Errorf("Expected foo=bar, got %s", val)
			}
			return nil
		},
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			stopCalled = true
			val, _ := envCtx.Get("foo")
			if val != "bar" {
				t.Errorf("Expected foo=bar in AfterStop, got %s", val)
			}
			return nil
		},
	}

	env := testrig.MustNew(testrig.With(s1), testrig.WithHooks(pm))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !startCalled {
		t.Error("AfterStart was not called")
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !stopCalled {
		t.Error("AfterStop was not called")
	}
}

func TestEnv_WithLifecycleHook_AfterStartError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	var stopCalled bool
	pm := &MockLifecycleHook{
		afterStart: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("start-fail")
		},
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			stopCalled = true
			return nil
		},
	}

	env := testrig.MustNew(testrig.With(s1), testrig.WithHooks(pm))

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from AfterStart, got nil")
	}
	if !strings.Contains(err.Error(), "start-fail") {
		t.Errorf("Expected error containing 'start-fail', got %v", err)
	}
	if !stopCalled {
		t.Error("AfterStop should have been called during rollback")
	}
}

func TestEnv_WithLifecycleHook_AfterStopError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	pm := &MockLifecycleHook{
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("stop-fail")
		},
	}

	env := testrig.MustNew(testrig.With(s1), testrig.WithHooks(pm))

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())
	if err == nil {
		t.Fatal("Expected error from AfterStop, got nil")
	}
	if !strings.Contains(err.Error(), "stop-fail") {
		t.Errorf("Expected error containing 'stop-fail', got %v", err)
	}
}

func TestEnvContext_Logger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &loggerCapturingService{
		MockService: MockService{name: "svc1"},
	}

	env := testrig.MustNew(testrig.With(svc), testrig.WithLogger(logger))
	_ = env.Start(context.Background())
	defer func() { _ = env.Stop(context.Background()) }()

	capturedLogger := svc.capturedEnvCtx.Logger()
	if capturedLogger == nil {
		t.Error("Expected a non-nil logger, got nil")
	}
	if capturedLogger == slog.Default() {
		t.Error("Expected a scoped child logger, got the bare slog default")
	}
}

type loggerCapturingService struct {
	MockService
	capturedEnvCtx testrig.EnvContext
}

func (s *loggerCapturingService) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	s.capturedEnvCtx = envCtx
	return nil, nil
}

func TestEnv_Stop_NotRunning(t *testing.T) {
	env := testrig.MustNew()
	if err := env.Stop(context.Background()); err != nil {
		t.Errorf("Stop on idle env should be no-op and return nil, got %v", err)
	}
}

func TestEnv_Properties_EmptyOnIdleEnv(t *testing.T) {
	// Properties() on a never-started env returns a non-nil but empty map.
	env := testrig.MustNew()
	if got := env.Properties(); got == nil {
		t.Error("expected non-nil empty map, got nil")
	} else if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestEnv_Properties_EmptyAfterStop(t *testing.T) {
	// Once the env stops, Properties() must return empty — the runState is
	// released so stale properties cannot leak to callers after Stop.
	svc := &MockService{name: "svc", properties: testrig.Properties{"k": "v"}}
	env := testrig.MustNew(testrig.With(svc))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if env.Properties()["k"] != "v" {
		t.Fatal("property k should be present while running")
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if got := env.Properties(); len(got) != 0 {
		t.Errorf("expected empty properties after Stop, got %v", got)
	}
}

func TestEnv_Restart_PropertiesReflectFreshRun(t *testing.T) {
	// Same Env instance, two Start/Stop cycles. The second run must not see
	// stale properties from the first — the runState is reset on each Start.
	svc := &MockService{name: "svc", properties: testrig.Properties{"k": "first"}}
	env := testrig.MustNew(testrig.With(svc))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if got := env.Properties()["k"]; got != "first" {
		t.Errorf("first run: got %q, want \"first\"", got)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}

	// Mutate the service so the second run publishes a different value.
	svc.properties = testrig.Properties{"k": "second"}

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()
	if got := env.Properties()["k"]; got != "second" {
		t.Errorf("second run: got %q, want \"second\"", got)
	}
}

func TestEnv_Stop_WithDependents(t *testing.T) {
	// A -> B; Stop should wait for A before stopping B.
	var stopOrder []string
	var mu sync.Mutex
	sB := &MockService{name: "B", onStop: func() { mu.Lock(); stopOrder = append(stopOrder, "B"); mu.Unlock() }}
	sA := &MockService{name: "A", deps: []string{"B"}, onStop: func() { mu.Lock(); stopOrder = append(stopOrder, "A"); mu.Unlock() }}

	env := testrig.MustNew(testrig.With(sA, sB))
	_ = env.Start(context.Background())
	_ = env.Stop(context.Background())

	if len(stopOrder) != 2 || stopOrder[0] != "A" || stopOrder[1] != "B" {
		t.Errorf("Unexpected stop order: %v", stopOrder)
	}
}

func TestEnv_Stop_ContextCancelled_WhileWaitingForDependent(t *testing.T) {
	// A -> B. During Stop, B's goroutine waits for A's stop signal because B
	// has A as a dependent. If the parent ctx cancels before A's slow Stop
	// finishes, B must surface the ctx error rather than block indefinitely.
	sB := &MockService{name: "B"}
	sA := &MockService{name: "A", deps: []string{"B"}, onStop: func() {
		time.Sleep(500 * time.Millisecond)
	}}

	env := testrig.MustNew(testrig.With(sA, sB))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := env.Stop(ctx)
	if err == nil {
		t.Fatal("expected error from ctx cancellation while waiting for dependent")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestEnv_Stop_MultipleHooksError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	h1Err := errors.New("err1")
	h2Err := errors.New("err2")
	h1 := &MockLifecycleHook{
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error { return h1Err },
	}
	h2 := &MockLifecycleHook{
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error { return h2Err },
	}

	env := testrig.MustNew(testrig.With(s1), testrig.WithHooks(h1, h2), testrig.WithLogger(logger))

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !errors.Is(err, h1Err) {
		t.Errorf("Expected returned error to wrap h1Err, got %v", err)
	}
	if !errors.Is(err, h2Err) {
		t.Errorf("Expected returned error to wrap h2Err, got %v", err)
	}
}

func TestEnv_Stop_ConcurrentCallsAreIdempotent(t *testing.T) {
	var stopCount, hookStopCount int32
	svc := &MockService{name: "svc1", onStop: func() { atomic.AddInt32(&stopCount, 1) }}
	hook := &MockLifecycleHook{
		afterStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			atomic.AddInt32(&hookStopCount, 1)
			return nil
		},
	}

	env := testrig.MustNew(testrig.With(svc), testrig.WithHooks(hook))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = env.Stop(context.Background())
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&stopCount); got != 1 {
		t.Errorf("svc.Stop called %d times under concurrent Stop; expected exactly 1", got)
	}
	if got := atomic.LoadInt32(&hookStopCount); got != 1 {
		t.Errorf("AfterStop hook called %d times under concurrent Stop; expected exactly 1", got)
	}
}

func TestEnvContext_TypeSafeHelpers(t *testing.T) {
	s1 := &MockService{
		name: "svc1",
		properties: testrig.Properties{
			"int":      "42",
			"bool":     "true",
			"duration": "1s",
		},
	}

	svc2 := &loggerCapturingService{
		MockService: MockService{name: "svc2", deps: []string{"svc1"}},
	}

	env := testrig.MustNew(testrig.With(s1, svc2))
	_ = env.Start(context.Background())
	defer func() { _ = env.Stop(context.Background()) }()

	capturedCtx := svc2.capturedEnvCtx

	if val, err := capturedCtx.Int("int"); err != nil || val != 42 {
		t.Errorf("Int() failed: val=%v, err=%v", val, err)
	}
	if val, err := capturedCtx.Bool("bool"); err != nil || val != true {
		t.Errorf("Bool() failed: val=%v, err=%v", val, err)
	}
	if val, err := capturedCtx.Duration("duration"); err != nil || val != time.Second {
		t.Errorf("Duration() failed: val=%v, err=%v", val, err)
	}
}

type scopedLoggerCapturingService struct {
	MockService
	capturedLogger *slog.Logger
}

func (s *scopedLoggerCapturingService) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	s.capturedLogger = envCtx.Logger()
	return nil, nil
}

func TestEnv_Start_ProvidesPerServiceScopedLogger(t *testing.T) {
	svc := &scopedLoggerCapturingService{
		MockService: MockService{name: "scoped-svc"},
	}

	env := testrig.MustNew(testrig.With(svc))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	if svc.capturedLogger == nil {
		t.Fatal("Logger was not injected into service Start")
	}
	if svc.capturedLogger == slog.Default() {
		t.Error("Expected per-service scoped logger, got bare default logger")
	}
}

func TestEnv_Start_DuplicateServiceName(t *testing.T) {
	s1 := &MockService{name: "dup-svc"}
	s2 := &MockService{name: "dup-svc"}

	env := testrig.MustNew(testrig.With(s1, s2))
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error for duplicate service name")
	}
	if !strings.Contains(err.Error(), "duplicate service name") {
		t.Errorf("Expected 'duplicate service name', got %v", err)
	}

	// Verify env returns to stateIdle — a fresh env with unique services succeeds.
	s3 := &MockService{name: "unique-svc"}
	env2 := testrig.MustNew(testrig.With(s3))
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("Start with unique services failed: %v", err)
	}
	_ = env2.Stop(context.Background())
}

func TestEnv_WithLogger_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithLogger(nil)")
		}
		if !strings.Contains(asString(r), "non-nil") {
			t.Errorf("Unexpected panic message: %v", r)
		}
	}()
	testrig.MustNew(testrig.WithLogger(nil))
}

func TestEnv_WithHooks_NilHookPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Expected panic from WithHooks(nil)")
		}
	}()
	testrig.MustNew(testrig.WithHooks(nil))
}

func TestEnv_WithHooks_NilInMiddlePanics(t *testing.T) {
	valid := &MockLifecycleHook{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithHooks(valid, nil)")
		}
		if !strings.Contains(asString(r), "index 1") {
			t.Errorf("Expected panic to mention index 1, got: %v", r)
		}
	}()
	testrig.MustNew(testrig.WithHooks(valid, nil))
}

func TestEnv_With_NilServicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Expected panic from With(nil)")
		}
	}()
	testrig.MustNew(testrig.With(nil))
}

func TestEnv_With_NilInMiddlePanics(t *testing.T) {
	valid := &MockService{name: "valid"}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from With(valid, nil)")
		}
		if !strings.Contains(asString(r), "index 1") {
			t.Errorf("Expected panic to mention index 1, got: %v", r)
		}
	}()
	testrig.MustNew(testrig.With(valid, nil))
}

func TestEnvBuilderBranching(t *testing.T) {
	svcA := &MockService{name: "a"}
	svcB := &MockService{name: "b"}
	svcC := &MockService{name: "c"}

	// Shared base options compose into independent envs via the option-list
	// pattern. Two envs built from a common base do not share configuration.
	baseOpts := []testrig.Option{testrig.With(svcA)}
	envA := testrig.MustNew(append(baseOpts, testrig.WithName("A"), testrig.With(svcB))...)
	envB := testrig.MustNew(append(baseOpts, testrig.WithName("B"), testrig.With(svcC))...)

	if envA.Name() != "A" {
		t.Errorf("envA.Name() = %q, want %q", envA.Name(), "A")
	}
	if envB.Name() != "B" {
		t.Errorf("envB.Name() = %q, want %q", envB.Name(), "B")
	}
}

// asString flattens any panic value to its string form.
func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return ""
	}
}
