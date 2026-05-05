package testrig_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/testrig-go/pkg/testrig"
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
func (m *MockService) Identifier() string     { return "mock:" + m.name }
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
	onStart func(ctx context.Context, envCtx testrig.EnvContext) error
	onStop  func(ctx context.Context, envCtx testrig.EnvContext) error
}

func (m *MockLifecycleHook) OnStart(ctx context.Context, envCtx testrig.EnvContext) error {
	if m.onStart != nil {
		return m.onStart(ctx, envCtx)
	}
	return nil
}

func (m *MockLifecycleHook) OnStop(ctx context.Context, envCtx testrig.EnvContext) error {
	if m.onStop != nil {
		return m.onStop(ctx, envCtx)
	}
	return nil
}

// --- Tests ---

func TestEnv_Start_Success(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"k1": "v1"}}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}, properties: testrig.Properties{"k2": "v2"}}

	env := testrig.New().With(s1, s2)

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
	s1 := &MockService{name: "svc-missing-dep", deps: []string{"missing-svc"}}

	env := testrig.New().With(s1)

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error due to missing dependency, got nil")
	}
	expected := "depends on unknown service missing-svc"
	if err.Error() != "service svc1 "+expected && err.Error() != expected { // Error group wrapping check
		if err.Error() != expected && !strings.Contains(err.Error(), expected) {
			t.Errorf("Expected error containing %q, got %q", expected, err.Error())
		}
	}
}

func TestEnv_Start_CircularDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", deps: []string{"svc2"}}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}}

	env := testrig.New().With(s1, s2)

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error due to circular dependency, got nil")
	}
	expected := "circular dependency detected"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Expected error containing %q, got %q", expected, err.Error())
	}
}

func TestEnv_Start_ServiceError(t *testing.T) {
	s1 := &MockService{name: "svc-start-err", startErr: errors.New("boom")}

	env := testrig.New().With(s1)

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

	env := testrig.New().With(s1)

	// Cancel immediately
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
	env := testrig.New().With(s1)

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

	// 4. Restart
	// NOTE: Because we are restarting, the service WILL be reused since we haven't cleaned up yet.
	// This is actually a good test of reuse.
	if err := env.Start(context.Background()); err != nil {
		t.Errorf("Restart failed: %v", err)
	}
	_ = env.Stop(context.Background())
}

func TestEnv_Start_ReuseWithProperties(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"p1": "v1"}}

	// Both envs share the same MapStore so env2 can discover what env1 published.
	sharedStore := testrig.NewMapStore()
	sharedDiscovery := testrig.NewDiscovery(sharedStore)

	env := testrig.New().With(s1).WithDiscovery(sharedDiscovery)

	// 1. Start env1 — publishes properties to sharedStore
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	// Keep env1 running so the service stays published.

	// 2. Start env2 with same service — should discover and reuse
	env2 := testrig.New().With(s1).WithDiscovery(sharedDiscovery)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("Second start failed: %v", err)
	}
	defer func() { _ = env2.Stop(context.Background()) }()
	defer func() { _ = env.Stop(context.Background()) }()

	props := env2.Properties()
	if props["p1"] != "v1" {
		t.Errorf("Expected p1=v1 from reused service, got %v", props)
	}
}

func TestEnv_Stop_ServiceError(t *testing.T) {
	s1 := &MockService{name: "svc-stop-err", stopErr: errors.New("stop-fail")}

	env := testrig.New().With(s1)

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
	env := testrig.New().WithLogger(logger)
	if env.Properties() == nil {
		t.Error("Properties should not be nil")
	}
}

func TestEnvDiscovery_Discover_InvalidJSON(t *testing.T) {
	d := testrig.NewCrossProcessDiscovery()
	s := &MockService{name: "invalid-json"}
	key := "TESTRIG_SERVICE_" + s.Identifier()
	_ = os.Setenv(key, "invalid-json")
	defer func() { _ = os.Unsetenv(key) }()

	props, found, err := d.Discover(context.Background(), s)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if !found {
		t.Error("Expected found to be true")
	}
	if len(props) != 0 {
		t.Errorf("Expected empty props, got %v", props)
	}
}

func TestEnv_Start_UnknownDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", deps: []string{"unknown"}}
	env := testrig.New().With(s1)

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error due to unknown dependency, got nil")
	}
	if !strings.Contains(err.Error(), "depends on unknown service unknown") {
		t.Errorf("Expected error containing 'depends on unknown service unknown', got %v", err)
	}
}

func TestEnv_Start_ContextCancellation_WaitingForDependency(t *testing.T) {
	s1 := &MockService{name: "svc1", startDelay: 1 * time.Second}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}}

	env := testrig.New().With(s1, s2)
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
	env := testrig.New().With(producer, consumer)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if env.Name() != "testenv" {
		t.Errorf("Unexpected env name: %s", env.Name())
	}

	_ = env.Stop(context.Background())
}

func TestEnv_Start_Rollback(t *testing.T) {
	var stopped1 bool
	s1 := &MockService{name: "svc1", onStop: func() { stopped1 = true }}
	s2 := &MockService{name: "svc2", startErr: errors.New("boom")}

	env := testrig.New().With(s1, s2)

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if !stopped1 {
		t.Error("svc1 should have been stopped (rollback)")
	}
}

func TestEnv_ParallelStartStop(t *testing.T) {
	// A -> B
	// C -> B
	// All should start/stop in parallel where possible.

	var mu sync.Mutex
	startTimes := make(map[string]time.Time)
	stopTimes := make(map[string]time.Time)

	recordStart := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		startTimes[name] = time.Now()
	}
	recordStop := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		stopTimes[name] = time.Now()
	}

	sB := &MockService{name: "B", startDelay: 50 * time.Millisecond, onStart: func() { recordStart("B") }, onStop: func() { recordStop("B") }}
	sA := &MockService{name: "A", deps: []string{"B"}, startDelay: 50 * time.Millisecond, onStart: func() { recordStart("A") }, onStop: func() { recordStop("A") }}
	sC := &MockService{name: "C", deps: []string{"B"}, startDelay: 50 * time.Millisecond, onStart: func() { recordStart("C") }, onStop: func() { recordStop("C") }}

	env := testrig.New().With(sA, sB, sC)

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify Start Order & Parallelism
	if startTimes["A"].Before(startTimes["B"]) || startTimes["C"].Before(startTimes["B"]) {
		t.Error("B must start before A and C")
	}
	// A and C should have started roughly at the same time (parallel)
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

	// Verify Stop Order & Parallelism
	if stopTimes["B"].Before(stopTimes["A"]) || stopTimes["B"].Before(stopTimes["C"]) {
		t.Error("A and C must stop before B")
	}
	// A and C should have stopped roughly at the same time (parallel)
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
		onStart: func(ctx context.Context, envCtx testrig.EnvContext) error {
			startCalled = true
			val, _ := envCtx.Get("foo")
			if val != "bar" {
				t.Errorf("Expected foo=bar, got %s", val)
			}
			return nil
		},
		onStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			stopCalled = true
			val, _ := envCtx.Get("foo")
			if val != "bar" {
				t.Errorf("Expected foo=bar in OnStop, got %s", val)
			}
			return nil
		},
	}

	env := testrig.New().With(s1).WithHooks(pm)

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !startCalled {
		t.Error("OnStart was not called")
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !stopCalled {
		t.Error("OnStop was not called")
	}
}

func TestEnv_WithLifecycleHook_OnStartError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	var stopCalled bool
	pm := &MockLifecycleHook{
		onStart: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("start-fail")
		},
		onStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			stopCalled = true
			return nil
		},
	}

	env := testrig.New().With(s1).WithHooks(pm)

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from OnStart, got nil")
	}
	if !strings.Contains(err.Error(), "start-fail") {
		t.Errorf("Expected error containing 'start-fail', got %v", err)
	}

	// If OnStart fails, Start should call Stop, which calls OnStop
	if !stopCalled {
		t.Error("OnStop should have been called during rollback")
	}
}

func TestEnv_WithLifecycleHook_OnStopError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	pm := &MockLifecycleHook{
		onStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("stop-fail")
		},
	}

	env := testrig.New().With(s1).WithHooks(pm)

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())
	if err == nil {
		t.Fatal("Expected error from OnStop, got nil")
	}
	if !strings.Contains(err.Error(), "stop-fail") {
		t.Errorf("Expected error containing 'stop-fail', got %v", err)
	}
}

func TestEnvContext_Logger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// We need a way to capture the envCtx.
	// Let's use a specialized mock service.
	svc := &loggerCapturingService{
		MockService: MockService{name: "svc1"},
	}

	env := testrig.New().With(svc).WithLogger(logger)
	_ = env.Start(context.Background())
	defer func() { _ = env.Stop(context.Background()) }()

	// Task 3.1: the captured logger is a scoped child of the env logger,
	// so it is NOT pointer-equal to the parent. It must be non-nil and
	// different from the bare slog default.
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
	env := testrig.New()
	if err := env.Stop(context.Background()); err != nil {
		t.Errorf("Stop on idle env should be no-op and return nil, got %v", err)
	}
}

func TestEnv_Stop_WithDependents(t *testing.T) {
	// A -> B
	// Stop should wait for A before stopping B
	var stopOrder []string
	var mu sync.Mutex
	sB := &MockService{name: "B", onStop: func() {
		mu.Lock()
		stopOrder = append(stopOrder, "B")
		mu.Unlock()
	}}
	sA := &MockService{name: "A", deps: []string{"B"}, onStop: func() {
		mu.Lock()
		stopOrder = append(stopOrder, "A")
		mu.Unlock()
	}}

	env := testrig.New().With(sA, sB)
	_ = env.Start(context.Background())
	_ = env.Stop(context.Background())

	if len(stopOrder) != 2 || stopOrder[0] != "A" || stopOrder[1] != "B" {
		t.Errorf("Unexpected stop order: %v", stopOrder)
	}
}

func TestEnv_Stop_MultipleHooksError(t *testing.T) {
	s1 := &MockService{name: "svc1"}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	h1 := &MockLifecycleHook{
		onStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("err1")
		},
	}
	h2 := &MockLifecycleHook{
		onStop: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("err2")
		},
	}

	env := testrig.New().With(s1).WithHooks(h1, h2).WithLogger(logger)

	_ = env.Start(context.Background())
	err := env.Stop(context.Background())

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	// First error should be returned, second logged.
	if !strings.Contains(err.Error(), "err1") {
		t.Errorf("Expected error containing 'err1', got %v", err)
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

	var capturedCtx testrig.EnvContext
	// We need a way to capture the context in s2.Start
	// Let's use a closure or a custom service.
	svc2 := &loggerCapturingService{
		MockService: MockService{name: "svc2", deps: []string{"svc1"}},
	}

	env := testrig.New().With(s1, svc2)
	_ = env.Start(context.Background())
	defer func() { _ = env.Stop(context.Background()) }()

	capturedCtx = svc2.capturedEnvCtx

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

// --- Task 1.2: Liveness Check ---

func TestEnvDiscovery_Discover_DeadHostPort(t *testing.T) {
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "dead-svc"}
	key := "TESTRIG_SERVICE_" + svc.Identifier()

	// Publish properties with a host+port that is guaranteed to be unreachable.
	deadProps := testrig.Properties{
		"dead-svc.host": "127.0.0.1",
		"dead-svc.port": "1", // Port 1 is privileged and never open in tests.
	}
	data, _ := json.Marshal(deadProps)
	_ = os.Setenv(key, string(data))
	defer func() { _ = os.Unsetenv(key) }()

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover returned unexpected error: %v", err)
	}
	if found {
		t.Error("Expected found=false for dead service, got true")
	}
	if props != nil {
		t.Errorf("Expected nil props for dead service, got %v", props)
	}
}

func TestEnvDiscovery_Discover_AliveHostPort(t *testing.T) {
	// Start a real listener to simulate a live service.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	addr := ln.Addr().(*net.TCPAddr)
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "live-svc"}
	key := "TESTRIG_SERVICE_" + svc.Identifier()

	liveProps := testrig.Properties{
		"live-svc.host": "127.0.0.1",
		"live-svc.port": fmt.Sprintf("%d", addr.Port),
	}
	data, _ := json.Marshal(liveProps)
	_ = os.Setenv(key, string(data))
	defer func() { _ = os.Unsetenv(key) }()

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover returned unexpected error: %v", err)
	}
	if !found {
		t.Error("Expected found=true for live service, got false")
	}
	if props == nil {
		t.Error("Expected non-nil props for live service")
	}
}

func TestEnvDiscovery_Discover_NoHostPort_IsLive(t *testing.T) {
	// Properties without host+port should be treated as live (backwards-compatible).
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "no-addr-svc"}
	key := "TESTRIG_SERVICE_" + svc.Identifier()

	noAddrProps := testrig.Properties{"some.key": "some.value"}
	data, _ := json.Marshal(noAddrProps)
	_ = os.Setenv(key, string(data))
	defer func() { _ = os.Unsetenv(key) }()

	_, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover returned unexpected error: %v", err)
	}
	if !found {
		t.Error("Expected found=true when no host/port in props, got false")
	}
}

// --- Task 1.3: Unpublish ---

func TestEnvDiscovery_Unpublish(t *testing.T) {
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "to-unpublish"}
	props := testrig.Properties{"k": "v"}

	_ = d.Publish(context.Background(), svc, props)
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	if os.Getenv(key) == "" {
		t.Fatal("Expected env var to be set after Publish")
	}

	if err := d.Unpublish(context.Background(), svc); err != nil {
		t.Fatalf("Unpublish returned error: %v", err)
	}
	if os.Getenv(key) != "" {
		t.Error("Expected env var to be unset after Unpublish")
	}
}

func TestEnv_Stop_Unpublishes_StartedService(t *testing.T) {
	svc := &MockService{name: "svc-to-unpublish", properties: testrig.Properties{"k": "v"}}
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	defer func() { _ = os.Unsetenv(key) }()

	env := testrig.New().With(svc).WithDiscovery(testrig.NewCrossProcessDiscovery())
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if os.Getenv(key) == "" {
		t.Fatal("Expected env var to be published after Start")
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if os.Getenv(key) != "" {
		t.Error("Expected env var to be unpublished after Stop")
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

	env := testrig.New().With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	if svc.capturedLogger == nil {
		t.Fatal("Logger was not injected into service Start")
	}
	// Logger should not be the bare default (it is scoped with service name).
	if svc.capturedLogger == slog.Default() {
		t.Error("Expected per-service scoped logger, got bare default logger")
	}
}

// --- DiscoveryStore-backed EnvDiscovery Tests ---

func TestEnvDiscovery_WithMapStore_PublishAndDiscover(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "map-svc", properties: testrig.Properties{"k": "v"}}

	if err := d.Publish(context.Background(), svc, svc.properties); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if !found {
		t.Error("Expected found=true")
	}
	if props["k"] != "v" {
		t.Errorf("Expected k=v, got %v", props)
	}
}

func TestEnvDiscovery_WithMapStore_Unpublish(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "map-unpub"}

	_ = d.Publish(context.Background(), svc, testrig.Properties{"k": "v"})
	_ = d.Unpublish(context.Background(), svc)

	_, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if found {
		t.Error("Expected found=false after unpublish")
	}
}

func TestEnvDiscovery_WithMapStore_Discover_NotFound(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "missing"}

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if found {
		t.Error("Expected found=false for empty store")
	}
	if props != nil {
		t.Errorf("Expected nil props, got %v", props)
	}
}

func TestEnvDiscovery_WithMapStore_Discover_InvalidJSON(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "bad-json"}

	key := "TESTRIG_SERVICE_" + svc.Identifier()
	_ = store.Store(key, "not-json")

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if !found {
		t.Error("Expected found=true for non-JSON value")
	}
	if len(props) != 0 {
		t.Errorf("Expected empty props, got %v", props)
	}
}

func TestNewEnvDiscovery_NilStore_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from NewDiscovery(nil)")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "non-nil DiscoveryStore") {
			t.Errorf("Unexpected panic message: %s", msg)
		}
	}()
	testrig.NewDiscovery(nil)
}

func TestEnvDiscovery_WithMapStore_Discover_DeadHostPort(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "dead-svc"}

	deadProps := testrig.Properties{
		"dead-svc.host": "127.0.0.1",
		"dead-svc.port": "1",
	}
	_ = d.Publish(context.Background(), svc, deadProps)

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover returned unexpected error: %v", err)
	}
	if found {
		t.Error("Expected found=false for dead service")
	}
	if props != nil {
		t.Errorf("Expected nil props, got %v", props)
	}
}

func TestEnvDiscovery_WithMapStore_Discover_AliveHostPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	addr := ln.Addr().(*net.TCPAddr)
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "live-svc"}

	liveProps := testrig.Properties{
		"live-svc.host": "127.0.0.1",
		"live-svc.port": fmt.Sprintf("%d", addr.Port),
	}
	_ = d.Publish(context.Background(), svc, liveProps)

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover returned unexpected error: %v", err)
	}
	if !found {
		t.Error("Expected found=true for live service")
	}
	if props == nil {
		t.Error("Expected non-nil props")
	}
}

func TestNewCrossProcessDiscovery_UsesOsEnv(t *testing.T) {
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "cross-proc"}
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	_ = d.Publish(context.Background(), svc, testrig.Properties{"k": "v"})
	val, ok := os.LookupEnv(key)
	if !ok {
		t.Fatal("Expected env var to be set after Publish")
	}
	if val == "" {
		t.Error("Expected non-empty env var value")
	}
}

// mockErrorStore is a DiscoveryStore that returns errors.
type mockErrorStore struct {
	storeErr  error
	deleteErr error
}

func (s *mockErrorStore) Load(key string) (string, bool) { return "", false }
func (s *mockErrorStore) Store(key, value string) error  { return s.storeErr }
func (s *mockErrorStore) Delete(key string) error        { return s.deleteErr }

func TestEnvDiscovery_Publish_StoreError(t *testing.T) {
	store := &mockErrorStore{storeErr: errors.New("store-fail")}
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "err-svc"}

	err := d.Publish(context.Background(), svc, testrig.Properties{"k": "v"})
	if err == nil {
		t.Fatal("Expected error from Publish")
	}
	if !strings.Contains(err.Error(), "err-svc") {
		t.Errorf("Expected error to contain service name, got %v", err)
	}
	if !strings.Contains(err.Error(), "store-fail") {
		t.Errorf("Expected error to contain cause, got %v", err)
	}
}

func TestEnvDiscovery_Unpublish_DeleteError(t *testing.T) {
	store := &mockErrorStore{deleteErr: errors.New("delete-fail")}
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "del-err-svc"}

	err := d.Unpublish(context.Background(), svc)
	if err == nil {
		t.Fatal("Expected error from Unpublish")
	}
	if !strings.Contains(err.Error(), "del-err-svc") {
		t.Errorf("Expected error to contain service name, got %v", err)
	}
	if !strings.Contains(err.Error(), "delete-fail") {
		t.Errorf("Expected error to contain cause, got %v", err)
	}
}

func TestEnvDiscovery_EmptyStringTreatedAsNotFound(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "empty-val"}

	key := "TESTRIG_SERVICE_" + svc.Identifier()
	_ = store.Store(key, "")

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if found {
		t.Error("Expected found=false for empty string value")
	}
	if props != nil {
		t.Errorf("Expected nil props, got %v", props)
	}
}

func TestEnvDiscovery_OsEnvStore_EmptyStringTreatedAsNotFound(t *testing.T) {
	d := testrig.NewCrossProcessDiscovery()
	svc := &MockService{name: "os-empty"}
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	_ = os.Setenv(key, "")

	props, found, err := d.Discover(context.Background(), svc)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if found {
		t.Error("Expected found=false for empty string env var")
	}
	if props != nil {
		t.Errorf("Expected nil props, got %v", props)
	}
}

func TestEnv_Start_DuplicateServiceName(t *testing.T) {
	s1 := &MockService{name: "dup-svc"}
	s2 := &MockService{name: "dup-svc"}

	env := testrig.New().With(s1, s2)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error for duplicate service name")
	}
	if !strings.Contains(err.Error(), "duplicate service name") {
		t.Errorf("Expected error containing 'duplicate service name', got %v", err)
	}

	// Verify env returns to stateIdle — a subsequent Start with fixed services succeeds.
	s3 := &MockService{name: "unique-svc"}
	env2 := testrig.New().With(s3)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("Start with unique services failed: %v", err)
	}
	_ = env2.Stop(context.Background())
}

func TestEnvDiscovery_LivenessCheck_IgnoresContextDeadline(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "deadline-svc"}

	deadProps := testrig.Properties{
		"deadline-svc.host": "127.0.0.1",
		"deadline-svc.port": "1", // Dead port
	}
	_ = d.Publish(context.Background(), svc, deadProps)

	// Use an already-expired context — Discover should still complete
	// normally (returning found=false) rather than failing with a context error.
	// This documents that the liveness check uses a hardcoded timeout,
	// not the caller's context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Wait for context to actually expire.
	<-ctx.Done()

	_, found, err := d.Discover(ctx, svc)
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if found {
		t.Error("Expected found=false for dead port")
	}
}

// --- Task 4: WithDiscovery builder tests ---

func TestEnv_WithDiscovery(t *testing.T) {
	store := testrig.NewMapStore()
	d := testrig.NewDiscovery(store)
	svc := &MockService{name: "wd-svc", properties: testrig.Properties{"k": "v"}}

	env := testrig.New().WithDiscovery(d).With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	// Verify published to the custom store, not OS env.
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	val, ok := store.Load(key)
	if !ok || val == "" {
		t.Error("Expected service to be published in custom store")
	}
}

func TestEnv_WithDiscovery_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithDiscovery(nil)")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "non-nil DiscoveryProvider") {
			t.Errorf("Unexpected panic message: %s", msg)
		}
	}()
	testrig.New().WithDiscovery(nil)
}

func TestEnv_WithDiscovery_CalledTwice_LastWins(t *testing.T) {
	store1 := testrig.NewMapStore()
	store2 := testrig.NewMapStore()
	d1 := testrig.NewDiscovery(store1)
	d2 := testrig.NewDiscovery(store2)
	svc := &MockService{name: "last-wins", properties: testrig.Properties{"k": "v"}}

	env := testrig.New().WithDiscovery(d1).WithDiscovery(d2).With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	key := "TESTRIG_SERVICE_" + svc.Identifier()
	_, ok1 := store1.Load(key)
	_, ok2 := store2.Load(key)
	if ok1 {
		t.Error("store1 should NOT have the service (d2 was last)")
	}
	if !ok2 {
		t.Error("store2 should have the service (d2 was last)")
	}
}

func TestEnv_DefaultDiscovery_NoOsEnvMutation(t *testing.T) {
	svc := &MockService{name: "no-env-svc", properties: testrig.Properties{"k": "v"}}

	beforeLen := len(os.Environ())

	env := testrig.New().With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Check no TESTRIG_SERVICE_* env vars after Start
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TESTRIG_SERVICE_") {
			t.Errorf("Found unexpected env var: %s", e)
		}
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Check no TESTRIG_SERVICE_* env vars after Stop
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TESTRIG_SERVICE_") {
			t.Errorf("Found unexpected env var after Stop: %s", e)
		}
	}

	afterLen := len(os.Environ())
	if afterLen > beforeLen {
		t.Errorf("os.Environ() grew: before=%d, after=%d", beforeLen, afterLen)
	}
}

func TestEnv_WithDiscovery_BuilderChain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := testrig.NewDiscovery(testrig.NewMapStore())
	svc := &MockService{name: "chain-svc"}

	// Verify builder chain returns *Env and works end-to-end.
	env := testrig.New().WithDiscovery(d).With(svc).WithLogger(logger)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	_ = env.Stop(context.Background())
}

func TestEnv_WithLogger_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithLogger(nil)")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "non-nil") {
			t.Errorf("Unexpected panic message: %s", msg)
		}
	}()
	testrig.New().WithLogger(nil)
}

func TestEnv_WithHooks_NilHookPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithHooks(nil)")
		}
	}()
	testrig.New().WithHooks(nil)
}

func TestEnv_WithHooks_NilInMiddlePanics(t *testing.T) {
	valid := &MockLifecycleHook{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from WithHooks(valid, nil)")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "index 1") {
			t.Errorf("Expected panic to mention index 1, got: %s", msg)
		}
	}()
	testrig.New().WithHooks(valid, nil)
}

func TestEnv_With_NilServicePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from With(nil)")
		}
	}()
	testrig.New().With(nil)
}

func TestEnv_With_NilInMiddlePanics(t *testing.T) {
	valid := &MockService{name: "valid"}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from With(valid, nil)")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "index 1") {
			t.Errorf("Expected panic to mention index 1, got: %s", msg)
		}
	}()
	testrig.New().With(valid, nil)
}

// --- Task 4: Integration tests ---

func TestEnv_Reuse_SharedMapStore(t *testing.T) {
	sharedStore := testrig.NewMapStore()
	svc := &MockService{name: "shared-svc", properties: testrig.Properties{"p": "val"}}

	env1 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc)
	if err := env1.Start(context.Background()); err != nil {
		t.Fatalf("env1 Start failed: %v", err)
	}
	// Keep env1 running so discovery data persists.

	env2 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("env2 Start failed: %v", err)
	}

	props := env2.Properties()
	if props["p"] != "val" {
		t.Errorf("Expected p=val from reused service, got %v", props)
	}

	_ = env2.Stop(context.Background())
	_ = env1.Stop(context.Background())
}

func TestEnv_Isolation_SeparateMapStores(t *testing.T) {
	svc := &MockService{name: "iso-svc", properties: testrig.Properties{"p": "val"}}

	env1 := testrig.New().With(svc)
	if err := env1.Start(context.Background()); err != nil {
		t.Fatalf("env1 Start failed: %v", err)
	}
	defer func() { _ = env1.Stop(context.Background()) }()

	// env2 uses default New() which has its own MapStore — should NOT discover env1's service.
	var started2 bool
	svc2 := &MockService{name: "iso-svc", properties: testrig.Properties{"p": "val2"}, onStart: func() { started2 = true }}
	env2 := testrig.New().With(svc2)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("env2 Start failed: %v", err)
	}
	defer func() { _ = env2.Stop(context.Background()) }()

	if !started2 {
		t.Error("svc2 should have started fresh (separate store), not been reused")
	}
}

// --- Immutability: builder copy-on-write ---

// TestEnvBuilderBranching verifies that builder methods (With, WithName, etc.)
// return independent copies of Env, so branching from a base Env does not
// cause shared mutable state between derived environments.
func TestEnvBuilderBranching(t *testing.T) {
	svcA := &MockService{name: "a"}
	svcB := &MockService{name: "b"}
	svcC := &MockService{name: "c"}

	base := testrig.New().With(svcA)
	envA := base.WithName("A").With(svcB)
	envB := base.WithName("B").With(svcC)

	// base must be unchanged
	baseProps := base.Properties() // triggers nil-safe internal state
	_ = baseProps
	if base.Name() != "testenv" {
		t.Errorf("base.Name() mutated: got %q, want %q", base.Name(), "testenv")
	}
	// envA and envB must have independent names
	if envA.Name() != "A" {
		t.Errorf("envA.Name() = %q, want %q", envA.Name(), "A")
	}
	if envB.Name() != "B" {
		t.Errorf("envB.Name() = %q, want %q", envB.Name(), "B")
	}

	// Start each env and verify independent service sets.
	if err := envA.Start(context.Background()); err != nil {
		t.Fatalf("envA.Start() failed: %v", err)
	}
	defer func() { _ = envA.Stop(context.Background()) }()

	// envB should start independently and add only svcC alongside the base svcA.
	if err := envB.Start(context.Background()); err != nil {
		t.Fatalf("envB.Start() failed: %v", err)
	}
	defer func() { _ = envB.Stop(context.Background()) }()
}

// TestEnvBuilderBranching_DefaultDiscoveryIsolation verifies that envs derived
// from the same base via copy-on-write get isolated discovery stores by default.
// Without isolation, envB would discover svcA from envA's store and skip Start.
func TestEnvBuilderBranching_DefaultDiscoveryIsolation(t *testing.T) {
	var startCount int
	svcA := &MockService{
		name:       "shared",
		properties: testrig.Properties{"k": "v"},
		onStart:    func() { startCount++ },
	}

	base := testrig.New().With(svcA)
	envA := base.WithName("A")
	envB := base.WithName("B")

	if err := envA.Start(context.Background()); err != nil {
		t.Fatalf("envA.Start() failed: %v", err)
	}
	defer func() { _ = envA.Stop(context.Background()) }()

	if err := envB.Start(context.Background()); err != nil {
		t.Fatalf("envB.Start() failed: %v", err)
	}
	defer func() { _ = envB.Stop(context.Background()) }()

	if startCount != 2 {
		t.Errorf("svcA.Start() called %d times, want 2 (each env should have isolated discovery)", startCount)
	}
}

// TestEnvBuilderBranching_ExplicitDiscoveryShared verifies that when a user
// explicitly sets a shared discovery via WithDiscovery, derived envs share it
// and reuse services as expected.
func TestEnvBuilderBranching_ExplicitDiscoveryShared(t *testing.T) {
	var startCount int
	svcA := &MockService{
		name:       "shared",
		properties: testrig.Properties{"k": "v"},
		onStart:    func() { startCount++ },
	}

	sharedStore := testrig.NewMapStore()
	base := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svcA)
	envA := base.WithName("A")
	envB := base.WithName("B")

	if err := envA.Start(context.Background()); err != nil {
		t.Fatalf("envA.Start() failed: %v", err)
	}
	defer func() { _ = envA.Stop(context.Background()) }()

	if err := envB.Start(context.Background()); err != nil {
		t.Fatalf("envB.Start() failed: %v", err)
	}
	defer func() { _ = envB.Stop(context.Background()) }()

	if startCount != 1 {
		t.Errorf("svcA.Start() called %d times, want 1 (explicit shared discovery should reuse)", startCount)
	}
}
