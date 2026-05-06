// Package testrig — internal tests for unexported types.
// These tests use package testrig (not testrig_test) so they can access
// unexported types directly.
package testrig

import (
	"testing"
)

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
