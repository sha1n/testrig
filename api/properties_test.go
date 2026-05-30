package api_test

import (
	"testing"

	"github.com/sha1n/testrig/api"
)

func TestProperties_BasicMapAccess(t *testing.T) {
	p := api.Properties{"k": "v"}
	if got := p["k"]; got != "v" {
		t.Errorf("Properties[\"k\"] = %q, want %q", got, "v")
	}
	if _, ok := p["missing"]; ok {
		t.Error("missing key should report ok=false")
	}
}

func TestProperties_Snapshot(t *testing.T) {
	p := api.Properties{"k": "v"}
	snap := p.Snapshot()
	if got := snap["k"]; got != "v" {
		t.Errorf("Snapshot[\"k\"] = %q, want %q", got, "v")
	}
	snap["k"] = "mutated"
	if got := p["k"]; got != "v" {
		t.Errorf("Original Properties[\"k\"] got mutated to %q", got)
	}
	p["k"] = "mutated_original"
	if got := snap["k"]; got != "mutated" {
		t.Errorf("Snapshot[\"k\"] got mutated to %q", got)
	}
}
