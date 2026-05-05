package testrig_test

import (
	"strings"
	"testing"

	"github.com/sha1n/testrig-go/pkg/testrig"
)

// These tests describe the functional-options public API:
// New(opts ...Option) (*Env, error). Validation happens inside the option;
// errors propagate from New rather than panicking.

func TestNew_NoOptions_Defaults(t *testing.T) {
	env, err := testrig.New()
	if err != nil {
		t.Fatalf("New() with no options should succeed; got %v", err)
	}
	if env == nil {
		t.Fatal("expected non-nil *Env")
	}
	if env.Name() != "testenv" {
		t.Errorf("default name = %q, want %q", env.Name(), "testenv")
	}
}

func TestNew_WithName_Empty_ReturnsError(t *testing.T) {
	env, err := testrig.New(testrig.WithName(""))
	if err == nil {
		t.Fatal("expected error from WithName(\"\")")
	}
	if env != nil {
		t.Error("expected nil *Env on error")
	}
	if !strings.Contains(err.Error(), "non-empty name") {
		t.Errorf("error should mention non-empty name; got %q", err.Error())
	}
}

func TestNew_WithDiscovery_Nil_ReturnsError(t *testing.T) {
	_, err := testrig.New(testrig.WithDiscovery(nil))
	if err == nil {
		t.Fatal("expected error from WithDiscovery(nil)")
	}
	if !strings.Contains(err.Error(), "non-nil DiscoveryProvider") {
		t.Errorf("error should mention non-nil DiscoveryProvider; got %q", err.Error())
	}
}

func TestNew_WithLogger_Nil_ReturnsError(t *testing.T) {
	_, err := testrig.New(testrig.WithLogger(nil))
	if err == nil {
		t.Fatal("expected error from WithLogger(nil)")
	}
	if !strings.Contains(err.Error(), "non-nil") {
		t.Errorf("error should mention non-nil; got %q", err.Error())
	}
}

func TestNew_WithHooks_Nil_ReturnsError(t *testing.T) {
	_, err := testrig.New(testrig.WithHooks(nil))
	if err == nil {
		t.Fatal("expected error from WithHooks(nil)")
	}
	if !strings.Contains(err.Error(), "nil LifecycleHook") {
		t.Errorf("error should mention nil LifecycleHook; got %q", err.Error())
	}
}

func TestNew_With_NilService_ReturnsError(t *testing.T) {
	_, err := testrig.New(testrig.With(nil))
	if err == nil {
		t.Fatal("expected error from With(nil)")
	}
	if !strings.Contains(err.Error(), "nil Service") {
		t.Errorf("error should mention nil Service; got %q", err.Error())
	}
}

func TestNew_OptionsAccumulate(t *testing.T) {
	logger1Called := false
	hook := &MockLifecycleHook{}
	svc := &MockService{name: "svc1"}

	env, err := testrig.New(
		testrig.WithName("custom"),
		testrig.WithHooks(hook),
		testrig.With(svc),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if env.Name() != "custom" {
		t.Errorf("name = %q, want %q", env.Name(), "custom")
	}
	_ = logger1Called // (placeholder to avoid declared-but-not-used in this minimal sketch)
}
