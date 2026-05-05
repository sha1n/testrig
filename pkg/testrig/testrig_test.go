package testrig_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/sha1n/testrig-go/pkg/testrig"
)

func TestProperties_TypeSafeHelpers(t *testing.T) {
	p := testrig.Properties{
		"int":      "42",
		"bool":     "true",
		"duration": "1s",
		"invalid":  "foo",
	}

	// Int
	if val, err := p.Int("int"); err != nil || val != 42 {
		t.Errorf("Int() failed: val=%v, err=%v", val, err)
	}
	if _, err := p.Int("missing"); err == nil {
		t.Error("Int() should have failed for missing key")
	}
	if _, err := p.Int("invalid"); err == nil {
		t.Error("Int() should have failed for invalid value")
	}

	// Bool
	if val, err := p.Bool("bool"); err != nil || val != true {
		t.Errorf("Bool() failed: val=%v, err=%v", val, err)
	}
	if _, err := p.Bool("missing"); err == nil {
		t.Error("Bool() should have failed for missing key")
	}
	if _, err := p.Bool("invalid"); err == nil {
		t.Error("Bool() should have failed for invalid value")
	}

	// Duration
	if val, err := p.Duration("duration"); err != nil || val != time.Second {
		t.Errorf("Duration() failed: val=%v, err=%v", val, err)
	}
	if _, err := p.Duration("missing"); err == nil {
		t.Error("Duration() should have failed for missing key")
	}
	if _, err := p.Duration("invalid"); err == nil {
		t.Error("Duration() should have failed for invalid value")
	}
}

func TestScopedLogger(t *testing.T) {
	parent := slog.Default()
	scoped := testrig.ScopedLogger(parent, "my-service")
	if scoped == nil {
		t.Error("ScopedLogger returned nil")
	}
	// Verify it's a different logger than the parent.
	if scoped == parent {
		t.Error("ScopedLogger should return a new logger, not the same instance")
	}
}
