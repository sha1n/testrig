package testrig_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/testrig-go/pkg/testrig"
)

// --- Mock DiscoveryProvider for error injection ---

type mockDiscoveryProvider struct {
	discoverErr  error
	publishErr   error
	unpublishErr error
	inner        testrig.DiscoveryProvider
}

func (m *mockDiscoveryProvider) Discover(ctx context.Context, svc testrig.Service) (testrig.Properties, bool, error) {
	if m.discoverErr != nil {
		return nil, false, m.discoverErr
	}
	return m.inner.Discover(ctx, svc)
}

func (m *mockDiscoveryProvider) Publish(ctx context.Context, svc testrig.Service, props testrig.Properties) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	return m.inner.Publish(ctx, svc, props)
}

func (m *mockDiscoveryProvider) Unpublish(ctx context.Context, svc testrig.Service) error {
	if m.unpublishErr != nil {
		return m.unpublishErr
	}
	return m.inner.Unpublish(ctx, svc)
}

// --- Full Lifecycle ---

func TestIntegration_FullLifecycle_DefaultIsolation(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: testrig.Properties{"a": "1"}}
	s2 := &MockService{name: "svc2", deps: []string{"svc1"}, properties: testrig.Properties{"b": "2"}}

	env := testrig.New().With(s1, s2)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	props := env.Properties()
	if props["a"] != "1" || props["b"] != "2" {
		t.Errorf("Unexpected properties: %v", props)
	}

	// Verify no OS env vars set.
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TESTRIG_SERVICE_") {
			t.Errorf("Found unexpected env var: %s", e)
		}
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestIntegration_FullLifecycle_CrossProcessDiscovery(t *testing.T) {
	svc := &MockService{name: "cross-svc", properties: testrig.Properties{"k": "v"}}
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	env := testrig.New().WithDiscovery(testrig.NewCrossProcessDiscovery()).With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		t.Error("Expected OS env var to be set after Start")
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	_, ok = os.LookupEnv(key)
	if ok {
		t.Error("Expected OS env var to be cleared after Stop")
	}
}

// --- Isolation ---

func TestIntegration_ParallelIsolation(t *testing.T) {
	t.Parallel()

	t.Run("env1", func(t *testing.T) {
		t.Parallel()
		svc := &MockService{name: "parallel-svc", properties: testrig.Properties{"x": "env1-val"}}
		env := testrig.New().With(svc)
		if err := env.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer func() { _ = env.Stop(context.Background()) }()

		if env.Properties()["x"] != "env1-val" {
			t.Error("env1 has wrong property value")
		}
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, "TESTRIG_SERVICE_") {
				t.Errorf("env1 leaked env var: %s", e)
			}
		}
	})

	t.Run("env2", func(t *testing.T) {
		t.Parallel()
		svc := &MockService{name: "parallel-svc", properties: testrig.Properties{"x": "env2-val"}}
		env := testrig.New().With(svc)
		if err := env.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer func() { _ = env.Stop(context.Background()) }()

		if env.Properties()["x"] != "env2-val" {
			t.Error("env2 has wrong property value")
		}
	})
}

func TestIntegration_SharedDiscovery_Reuse(t *testing.T) {
	sharedStore := testrig.NewMapStore()
	svc := &MockService{name: "shared-reuse", properties: testrig.Properties{"p": "val"}}

	env1 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc)
	if err := env1.Start(context.Background()); err != nil {
		t.Fatalf("env1 Start failed: %v", err)
	}

	env2 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("env2 Start failed: %v", err)
	}

	if env2.Properties()["p"] != "val" {
		t.Error("env2 should have reused properties from env1")
	}

	_ = env2.Stop(context.Background())
	_ = env1.Stop(context.Background())
}

func TestIntegration_SharedDiscovery_Unpublish_Prevents_Reuse(t *testing.T) {
	sharedStore := testrig.NewMapStore()
	var started2 bool
	svc := &MockService{name: "unpub-reuse", properties: testrig.Properties{"p": "val"}}

	env1 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc)
	if err := env1.Start(context.Background()); err != nil {
		t.Fatalf("env1 Start failed: %v", err)
	}
	// Stop unpublishes the service.
	_ = env1.Stop(context.Background())

	svc2 := &MockService{name: "unpub-reuse", properties: testrig.Properties{"p": "fresh"}, onStart: func() { started2 = true }}
	env2 := testrig.New().WithDiscovery(testrig.NewDiscovery(sharedStore)).With(svc2)
	if err := env2.Start(context.Background()); err != nil {
		t.Fatalf("env2 Start failed: %v", err)
	}
	defer func() { _ = env2.Stop(context.Background()) }()

	if !started2 {
		t.Error("svc2 should have started fresh (env1 unpublished)")
	}
}

// --- Error Handling ---

func TestIntegration_ServiceStartError_PartialRollback(t *testing.T) {
	var stopped1 bool
	s1 := &MockService{name: "svc1", onStop: func() { stopped1 = true }}
	s2 := &MockService{name: "svc2", startErr: errors.New("fail")}

	env := testrig.New().With(s1, s2)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error")
	}
	if !stopped1 {
		t.Error("svc1 should have been stopped (rollback)")
	}
}

func TestIntegration_DiscoveryPublishError(t *testing.T) {
	var stopped bool
	svc := &MockService{name: "pub-err-svc", properties: testrig.Properties{"k": "v"}, onStop: func() { stopped = true }}

	dp := &mockDiscoveryProvider{
		publishErr: errors.New("publish-fail"),
		inner:      testrig.NewDiscovery(testrig.NewMapStore()),
	}

	env := testrig.New().WithDiscovery(dp).With(svc)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from Publish")
	}
	if !strings.Contains(err.Error(), "publish-fail") {
		t.Errorf("Expected publish error, got %v", err)
	}
	if !stopped {
		t.Error("Service should have been rolled back")
	}
}

func TestIntegration_DiscoveryUnpublishError(t *testing.T) {
	svc := &MockService{name: "unpub-err-svc", properties: testrig.Properties{"k": "v"}}

	dp := &mockDiscoveryProvider{
		unpublishErr: errors.New("unpublish-fail"),
		inner:        testrig.NewDiscovery(testrig.NewMapStore()),
	}

	env := testrig.New().WithDiscovery(dp).With(svc)
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err := env.Stop(context.Background())
	if err == nil {
		t.Fatal("Expected error from Stop")
	}
	if !strings.Contains(err.Error(), "unpublish-fail") {
		t.Errorf("Expected unpublish error, got %v", err)
	}
}

// --- Edge Cases ---

func TestIntegration_NoServices(t *testing.T) {
	env := testrig.New()
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	props := env.Properties()
	if len(props) != 0 {
		t.Errorf("Expected empty properties, got %v", props)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestIntegration_ConcurrentStartCalls(t *testing.T) {
	svc := &MockService{name: "conc-svc", startDelay: 50 * time.Millisecond}

	env := testrig.New().With(svc)

	var wg sync.WaitGroup
	var successes, failures atomic.Int32

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if err := env.Start(context.Background()); err != nil {
				failures.Add(1)
			} else {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Errorf("Expected exactly 1 success, got %d", successes.Load())
	}
	if failures.Load() != 1 {
		t.Errorf("Expected exactly 1 failure, got %d", failures.Load())
	}

	_ = env.Stop(context.Background())
}

func TestIntegration_ContextCancelDuringStart(t *testing.T) {
	svc := &MockService{name: "slow-cancel-svc", startDelay: 5 * time.Second}

	env := testrig.New().With(svc)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := env.Start(ctx)
	if err == nil {
		t.Fatal("Expected error from context cancellation")
	}

	// Env should be back to idle after rollback.
	if err := env.Stop(context.Background()); err != nil {
		t.Logf("Stop after cancel returned: %v (acceptable)", err)
	}
}

func TestIntegration_StartRetryAfterFailure(t *testing.T) {
	svc := &MockService{name: "retry-svc", startErr: errors.New("temp-fail")}

	env := testrig.New().With(svc)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected first Start to fail")
	}

	// Clear the error and retry.
	svc.startErr = nil
	svc.properties = testrig.Properties{"k": "v"}

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Second Start should succeed, got %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	if env.Properties()["k"] != "v" {
		t.Error("Expected properties from second Start")
	}
}

func TestIntegration_RestartClearsOldProperties(t *testing.T) {
	svc1 := &MockService{name: "clear-svc", properties: testrig.Properties{"a": "1"}}

	base := testrig.New().With(svc1)
	if err := base.Start(context.Background()); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	_ = base.Stop(context.Background())

	// Build a new env that includes both services.
	// With copy-on-write semantics, With() returns a new *Env — assign it.
	svc2 := &MockService{name: "clear-svc2", properties: testrig.Properties{"b": "2"}}
	env := base.With(svc2)

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	props := env.Properties()
	if props["b"] != "2" {
		t.Error("Expected property b from second run")
	}
	// Property "a" should be present too since svc1 is still registered.
	if props["a"] != "1" {
		t.Error("Expected property a from svc1 still in env")
	}
}

// --- OnStart Hook Failure Triggers Rollback ---

func TestIntegration_OnStartHookFailure_RollsBackServices(t *testing.T) {
	var stopped bool
	svc := &MockService{name: "hook-fail-svc", properties: testrig.Properties{"k": "v"}, onStop: func() { stopped = true }}

	hook := &MockLifecycleHook{
		onStart: func(ctx context.Context, envCtx testrig.EnvContext) error {
			return errors.New("hook-fail")
		},
	}

	env := testrig.New().With(svc).WithHooks(hook)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from hook failure")
	}
	if !strings.Contains(err.Error(), "hook-fail") {
		t.Errorf("Expected hook-fail in error, got %v", err)
	}
	if !stopped {
		t.Error("Service should have been stopped (rollback) after hook failure")
	}
}

// --- Discovery Error Causes Service Start Failure ---

func TestIntegration_DiscoverError_FailsStart(t *testing.T) {
	s1 := &MockService{name: "disc-err-svc", properties: testrig.Properties{"k": "v"}}

	dp := &mockDiscoveryProvider{
		discoverErr: errors.New("discover-fail"),
		inner:       testrig.NewDiscovery(testrig.NewMapStore()),
	}

	env := testrig.New().WithDiscovery(dp).With(s1)
	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from discovery failure")
	}
	if !strings.Contains(err.Error(), "discover-fail") {
		t.Errorf("Expected discover-fail in error, got %v", err)
	}
}

// --- WithName ---

func TestIntegration_WithName(t *testing.T) {
	env := testrig.New().WithName("my-custom-env")
	if env.Name() != "my-custom-env" {
		t.Errorf("Expected name 'my-custom-env', got %q", env.Name())
	}
}

func TestIntegration_WithName_EmptyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic from empty name")
		}
	}()
	testrig.New().WithName("")
}

func TestIntegration_WithName_AppearsInError(t *testing.T) {
	env := testrig.New().WithName("named-env")
	svc := &MockService{name: "svc1"}
	// With copy-on-write, With() returns a new *Env — assign it.
	env = env.With(svc)

	// Start the env, then try to start again — error should contain the name
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from double start")
	}
	if !strings.Contains(err.Error(), "named-env") {
		t.Errorf("Expected env name in error, got %v", err)
	}
}
