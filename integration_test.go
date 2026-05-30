package testrig_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/api"
)

// --- Full Lifecycle ---

func TestIntegration_FullLifecycle(t *testing.T) {
	s1 := &MockService{name: "svc1", properties: api.Properties{"a": "1"}}
	s2 := &MockService{name: "svc2", properties: api.Properties{"b": "2"}}

	env := testrig.New("test").With(s1, s2)
	props, err := env.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if props["a"] != "1" || props["b"] != "2" {
		t.Errorf("Unexpected properties: %v", props)
	}

	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// --- Isolation ---

func TestIntegration_ParallelEnvsAreIndependent(t *testing.T) {
	t.Parallel()

	t.Run("env1", func(t *testing.T) {
		t.Parallel()
		svc := &MockService{name: "parallel-svc", properties: api.Properties{"x": "env1-val"}}
		env := testrig.New("test").With(svc)
		props, err := env.Start(context.Background())
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer func() { _ = env.Stop(context.Background()) }()

		if props["x"] != "env1-val" {
			t.Error("env1 has wrong property value")
		}
	})

	t.Run("env2", func(t *testing.T) {
		t.Parallel()
		svc := &MockService{name: "parallel-svc", properties: api.Properties{"x": "env2-val"}}
		env := testrig.New("test").With(svc)
		props, err := env.Start(context.Background())
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer func() { _ = env.Stop(context.Background()) }()

		if props["x"] != "env2-val" {
			t.Error("env2 has wrong property value")
		}
	})
}

// --- Error Handling ---

func TestIntegration_ServiceStartError_PartialRollback(t *testing.T) {
	var stopped1 bool
	s1 := &MockService{name: "svc1", onStop: func() { stopped1 = true }}
	s2 := &MockService{name: "svc2", startErr: errors.New("fail")}

	env := testrig.New("test").With(s1, s2)
	_, err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error")
	}
	if !stopped1 {
		t.Error("svc1 should have been stopped (rollback)")
	}
}

// --- Edge Cases ---

func TestIntegration_NoServices(t *testing.T) {
	env := testrig.New("test")
	props, err := env.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if len(props) != 0 {
		t.Errorf("Expected empty properties, got %v", props)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestIntegration_ConcurrentStartCalls(t *testing.T) {
	svc := &MockService{name: "conc-svc", startDelay: 50 * time.Millisecond}

	env := testrig.New("test").With(svc)

	var wg sync.WaitGroup
	var successes, failures atomic.Int32

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if _, err := env.Start(context.Background()); err != nil {
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

	env := testrig.New("test").With(svc)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := env.Start(ctx)
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

	env := testrig.New("test").With(svc)
	_, err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected first Start to fail")
	}

	svc.startErr = nil
	svc.properties = api.Properties{"k": "v"}

	props, err := env.Start(context.Background())
	if err != nil {
		t.Fatalf("Second Start should succeed, got %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	if props["k"] != "v" {
		t.Error("Expected properties from second Start")
	}
}

func TestIntegration_FreshEnv_DoesNotInheritFromPriorEnv(t *testing.T) {
	// Two independent Envs sharing a Service instance still publish properties
	// independently — the second env's view contains exactly its own services,
	// no leak from the first env's run.
	svc1 := &MockService{name: "shared-svc", properties: api.Properties{"a": "1"}}
	svc2 := &MockService{name: "second-only", properties: api.Properties{"b": "2"}}

	first := testrig.New("test").With(svc1)
	if _, err := first.Start(context.Background()); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	_ = first.Stop(context.Background())

	env := testrig.New("test").With(svc1, svc2)
	props, err := env.Start(context.Background())
	if err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	if props["a"] != "1" {
		t.Error("Expected property a from svc1")
	}
	if props["b"] != "2" {
		t.Error("Expected property b from svc2")
	}
}

// --- AfterStart Hook Failure Triggers Rollback ---

func TestIntegration_AfterStartHookFailure_RollsBackServices(t *testing.T) {
	var stopped bool
	svc := &MockService{name: "hook-fail-svc", properties: api.Properties{"k": "v"}, onStop: func() { stopped = true }}

	hook := &MockLifecycleHook{
		afterStart: func(ctx context.Context, props api.Properties, logger *slog.Logger) error {
			return errors.New("hook-fail")
		},
	}

	env := testrig.New("test").With(svc).WithLifecycleHooks(hook)
	_, err := env.Start(context.Background())
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

// --- WithName ---

func TestIntegration_WithName(t *testing.T) {
	env := testrig.New("my-custom-env")
	if env.Name() != "my-custom-env" {
		t.Errorf("Expected name 'my-custom-env', got %q", env.Name())
	}
}

func TestIntegration_WithName_AppearsInError(t *testing.T) {
	svc := &MockService{name: "svc1"}
	env := testrig.New("named-env").With(svc)

	if _, err := env.Start(context.Background()); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	_, err := env.Start(context.Background())
	if err == nil {
		t.Fatal("Expected error from double start")
	}
	if !strings.Contains(err.Error(), "named-env") {
		t.Errorf("Expected env name in error, got %v", err)
	}
}
