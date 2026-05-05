// Package testrig — internal tests for unexported types.
// These tests use package testrig (not testrig_test) so they can access
// unexported types directly.
package testrig

import (
	"testing"
)

// --- mapStore zero-value ---

func TestMapStore_ZeroValue_Safe(t *testing.T) {
	s := &mapStore{}
	if err := s.Store("k", "v"); err != nil {
		t.Fatalf("Store on zero-value mapStore failed: %v", err)
	}
	val, ok := s.Load("k")
	if !ok || val != "v" {
		t.Errorf("Expected k=v, got ok=%v val=%q", ok, val)
	}
}

func TestMapStore_ZeroValue_LoadOnly(t *testing.T) {
	s := &mapStore{}
	_, ok := s.Load("missing")
	if ok {
		t.Error("Expected ok=false on zero-value mapStore")
	}
}

// --- envState String ---

func TestEnvState_String(t *testing.T) {
	cases := []struct {
		state envState
		want  string
	}{
		{stateIdle, "idle"},
		{stateStarting, "starting"},
		{stateRunning, "running"},
		{stateStopping, "stopping"},
		{envState(99), "envState(99)"}, // unknown values surface as such, not silently as "0"
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("envState(%d).String() = %q, want %q", c.state, got, c.want)
		}
	}
}
