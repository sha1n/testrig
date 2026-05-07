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
	startErr   error
	stopErr    error
	startDelay time.Duration
	stopDelay  time.Duration
	properties testrig.Properties
	onStart    func()
	onStop     func()
}

func (m *MockService) Name() string { return m.name }

func (m *MockService) Start(ctx context.Context, logger *slog.Logger) (testrig.Properties, error) {
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
	if m.stopDelay > 0 {
		select {
		case <-time.After(m.stopDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.onStop != nil {
		m.onStop()
	}
	return m.stopErr
}

type MockLifecycleHook struct {
	afterStart func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error
	afterStop  func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error
}

func (m *MockLifecycleHook) AfterStart(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
	if m.afterStart != nil {
		return m.afterStart(ctx, props, logger)
	}
	return nil
}

func (m *MockLifecycleHook) AfterStop(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
	if m.afterStop != nil {
		return m.afterStop(ctx, props, logger)
	}
	return nil
}

// --- Tests ---

func TestEnv_Start_Success(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"k1": "v1"}}
	s2 := &MockService{name: "svc2", properties: testrig.Properties{"k2": "v2"}}

	env := testrig.New("test").With(s1, s2)

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	props := env.Properties()
	if props["k1"] != "v1" || props["k2"] != "v2" {
		t.Errorf("Unexpected properties: %v", props)
	}
}

func TestEnv_Start_ServiceError(t *testing.T) {
	s1 := &MockService{name: "svc1", startErr: errors.New("boom")}

	env := testrig.New("test").With(s1)

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

	env := testrig.New("test").With(s1)
	cancel()

	err := env.Start(ctx)
	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Expected context canceled, got %v", err)
	}
}

func TestEnv_StateTransitions(t *testing.T) {
	s1 := &MockService{name: "svc-state"}
	env := testrig.New("test").With(s1)

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

	env := testrig.New("test").With(s1)

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
	env := testrig.New("test").WithLogger(logger)
	if env.Properties() == nil {
		t.Error("Properties should not be nil")
	}
}

func TestEnv_Start_Rollback(t *testing.T) {
	// Two services: s1 starts successfully, s2 fails. Rollback must stop s1
	// (it acquired resources) but must NOT call Stop on s2 (it never started).
	var stopped1, stopped2 bool
	s1 := &MockService{name: "svc1", onStop: func() { stopped1 = true }}
	s2 := &MockService{name: "svc2", startErr: errors.New("boom"), onStop: func() { stopped2 = true }}

	env := testrig.New("test").With(s1, s2)

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

func TestEnv_Start_Rollback_JoinsRollbackErrors(t *testing.T) {
	startErr := errors.New("boom")
	stopErr := errors.New("rollback-fail")
	s1 := &MockService{name: "s1", stopErr: stopErr}
	s2 := &MockService{name: "s2", startErr: startErr}

	env := testrig.New("test").With(s1, s2)
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
	// All services start in parallel and stop in parallel — no dependency
	// ordering. Three services with identical 50ms delays should all
	// start within ~tens of ms of each other.
	var mu sync.Mutex
	startTimes := make(map[string]time.Time)
	stopTimes := make(map[string]time.Time)

	recordStart := func(name string) { mu.Lock(); startTimes[name] = time.Now(); mu.Unlock() }
	recordStop := func(name string) { mu.Lock(); stopTimes[name] = time.Now(); mu.Unlock() }

	mkSvc := func(name string) *MockService {
		return &MockService{
			name:       name,
			startDelay: 50 * time.Millisecond,
			stopDelay:  50 * time.Millisecond,
			onStart:    func() { recordStart(name) },
			onStop:     func() { recordStop(name) },
		}
	}

	env := testrig.New("test").With(mkSvc("A"), mkSvc("B"), mkSvc("C"))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	startSpread := startTimes["C"].Sub(startTimes["A"])
	if startSpread < 0 {
		startSpread = -startSpread
	}
	if startSpread > 40*time.Millisecond {
		t.Errorf("services did not start in parallel: spread=%v", startSpread)
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	stopSpread := stopTimes["C"].Sub(stopTimes["A"])
	if stopSpread < 0 {
		stopSpread = -stopSpread
	}
	if stopSpread > 40*time.Millisecond {
		t.Errorf("services did not stop in parallel: spread=%v", stopSpread)
	}
}

func TestEnv_WithLifecycleHook_Success(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"foo": "bar"}}

	var startCalled, stopCalled bool
	pm := &MockLifecycleHook{
		afterStart: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			startCalled = true
			if props["foo"] != "bar" {
				t.Errorf("Expected foo=bar, got %s", props["foo"])
			}
			return nil
		},
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			stopCalled = true
			if props["foo"] != "bar" {
				t.Errorf("Expected foo=bar in AfterStop, got %s", props["foo"])
			}
			return nil
		},
	}

	env := testrig.New("test").With(s1).WithLifecycleHooks(pm)

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
		afterStart: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			return errors.New("start-fail")
		},
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			stopCalled = true
			return nil
		},
	}

	env := testrig.New("test").With(s1).WithLifecycleHooks(pm)

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
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			return errors.New("stop-fail")
		},
	}

	env := testrig.New("test").With(s1).WithLifecycleHooks(pm)

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())
	if err == nil {
		t.Fatal("Expected error from AfterStop, got nil")
	}
	if !strings.Contains(err.Error(), "stop-fail") {
		t.Errorf("Expected error containing 'stop-fail', got %v", err)
	}
}

func TestEnv_PerServiceScopedLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := &loggerCapturingService{
		MockService: MockService{name: "svc1"},
	}

	env := testrig.New("test").With(svc).WithLogger(logger)
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

type loggerCapturingService struct {
	MockService
	capturedLogger *slog.Logger
}

func (s *loggerCapturingService) Start(ctx context.Context, logger *slog.Logger) (testrig.Properties, error) {
	s.capturedLogger = logger
	return nil, nil
}

func TestEnv_Stop_NotRunning(t *testing.T) {
	env := testrig.New("test")
	if err := env.Stop(context.Background()); err != nil {
		t.Errorf("Stop on idle env should be no-op and return nil, got %v", err)
	}
}

func TestEnv_Properties_EmptyOnIdleEnv(t *testing.T) {
	// Properties() on a never-started env returns a non-nil but empty map.
	env := testrig.New("test")
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
	env := testrig.New("test").With(svc)

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
	env := testrig.New("test").With(svc)

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

func TestEnv_Stop_MultipleHooksError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	h1Err := errors.New("err1")
	h2Err := errors.New("err2")
	h1 := &MockLifecycleHook{
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error { return h1Err },
	}
	h2 := &MockLifecycleHook{
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error { return h2Err },
	}

	env := testrig.New("test").With(s1).WithLifecycleHooks(h1, h2).WithLogger(logger)

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
		afterStop: func(ctx context.Context, props testrig.Properties, logger *slog.Logger) error {
			atomic.AddInt32(&hookStopCount, 1)
			return nil
		},
	}

	env := testrig.New("test").With(svc).WithLifecycleHooks(hook)
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

func TestEnv_Start_DuplicateServiceName(t *testing.T) {
	s1 := &MockService{name: "dup-svc"}
	s2 := &MockService{name: "dup-svc"}

	env := testrig.New("test").With(s1, s2)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error for duplicate service name")
	}
	if !strings.Contains(err.Error(), "duplicate service name") {
		t.Errorf("Expected 'duplicate service name', got %v", err)
	}

	// Verify env returns to stateIdle — a fresh env with unique services succeeds.
	s3 := &MockService{name: "unique-svc"}
	env2 := testrig.New("test").With(s3)
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
	testrig.New("test").WithLogger(nil)
}

func TestEnv_WithLifecycleHooks_NilHookPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Expected panic from WithLifecycleHooks(nil)")
		}
	}()
	testrig.New("test").WithLifecycleHooks(nil)
}

func TestEnv_WithLifecycleHooks_NilInMiddlePanics(t *testing.T) {
	valid := &MockLifecycleHook{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithLifecycleHooks(valid, nil)")
		}
		if !strings.Contains(asString(r), "index 1") {
			t.Errorf("Expected panic to mention index 1, got: %v", r)
		}
	}()
	testrig.New("test").WithLifecycleHooks(valid, nil)
}

func TestEnv_With_NilServicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Expected panic from With(nil)")
		}
	}()
	testrig.New("test").With(nil)
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
	testrig.New("test").With(valid, nil)
}

func TestEnv_NameSetByConstructor(t *testing.T) {
	env := testrig.New("my-env")
	if env.Name() != "my-env" {
		t.Errorf("env.Name() = %q, want %q", env.Name(), "my-env")
	}
}

func TestEnv_WithStages_RunsStagesInOrder(t *testing.T) {
	// Two-stage track: A starts first, then B and C start concurrently
	// after A is up. Verify ordering by recording start timestamps.
	var mu sync.Mutex
	startTimes := make(map[string]time.Time)
	recordStart := func(name string) { mu.Lock(); startTimes[name] = time.Now(); mu.Unlock() }

	mkSvc := func(name string) *MockService {
		return &MockService{
			name:       name,
			startDelay: 50 * time.Millisecond,
			onStart:    func() { recordStart(name) },
		}
	}
	a, b, c := mkSvc("A"), mkSvc("B"), mkSvc("C")

	env := testrig.New("staged").WithStages(testrig.NewStages(a).Then(b, c))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	// A must finish starting before B and C start.
	if !startTimes["A"].Before(startTimes["B"]) {
		t.Errorf("A should start before B; A=%v B=%v", startTimes["A"], startTimes["B"])
	}
	if !startTimes["A"].Before(startTimes["C"]) {
		t.Errorf("A should start before C; A=%v C=%v", startTimes["A"], startTimes["C"])
	}

	// B and C should start within ~tens of ms of each other (parallel
	// within stage 2).
	spread := startTimes["C"].Sub(startTimes["B"])
	if spread < 0 {
		spread = -spread
	}
	if spread > 40*time.Millisecond {
		t.Errorf("B and C should start in parallel within stage 2; spread=%v", spread)
	}
}

func TestEnv_WithStages_StopsInReverseStageOrder(t *testing.T) {
	// Two-stage track: stage 1 = {A}, stage 2 = {B, C}. On Stop, stage 2
	// must finish before stage 1.
	var mu sync.Mutex
	stopTimes := make(map[string]time.Time)
	recordStop := func(name string) { mu.Lock(); stopTimes[name] = time.Now(); mu.Unlock() }

	mkSvc := func(name string) *MockService {
		return &MockService{
			name:      name,
			stopDelay: 50 * time.Millisecond,
			onStop:    func() { recordStop(name) },
		}
	}
	a, b, c := mkSvc("A"), mkSvc("B"), mkSvc("C")

	env := testrig.New("staged-stop").WithStages(testrig.NewStages(a).Then(b, c))
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// A's Stop runs after B's and C's Stops.
	if !stopTimes["A"].After(stopTimes["B"]) {
		t.Errorf("A should stop after B; A=%v B=%v", stopTimes["A"], stopTimes["B"])
	}
	if !stopTimes["A"].After(stopTimes["C"]) {
		t.Errorf("A should stop after C; A=%v C=%v", stopTimes["A"], stopTimes["C"])
	}
}

func TestEnv_WithStages_NilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Expected panic from WithStages(nil)")
		}
	}()
	testrig.New("test").WithStages(nil)
}

func TestEnv_TracksRunInParallel(t *testing.T) {
	// Two independent tracks (each single-stage, single service) must
	// start concurrently — same wall-clock parallelism as if both were
	// in one With call.
	var mu sync.Mutex
	startTimes := make(map[string]time.Time)
	recordStart := func(name string) { mu.Lock(); startTimes[name] = time.Now(); mu.Unlock() }

	mkSvc := func(name string) *MockService {
		return &MockService{
			name:       name,
			startDelay: 50 * time.Millisecond,
			onStart:    func() { recordStart(name) },
		}
	}
	a, b := mkSvc("A"), mkSvc("B")

	env := testrig.New("two-tracks").With(a).With(b)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	spread := startTimes["B"].Sub(startTimes["A"])
	if spread < 0 {
		spread = -spread
	}
	if spread > 40*time.Millisecond {
		t.Errorf("two tracks did not start in parallel; spread=%v", spread)
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
