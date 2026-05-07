package testrig_test

import (
	"testing"

	"github.com/sha1n/testrig"
)

func TestProperties_BasicMapAccess(t *testing.T) {
	p := testrig.Properties{"k": "v"}
	if got := p["k"]; got != "v" {
		t.Errorf("Properties[\"k\"] = %q, want %q", got, "v")
	}
	if _, ok := p["missing"]; ok {
		t.Error("missing key should report ok=false")
	}
}
