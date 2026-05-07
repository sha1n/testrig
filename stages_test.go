package testrig_test

import (
	"strings"
	"testing"

	"github.com/sha1n/testrig"
)

func TestNewStages_BasicShape(t *testing.T) {
	a := &MockService{name: "a"}
	b := &MockService{name: "b"}
	s := testrig.NewStages(a, b)
	if s == nil {
		t.Fatal("NewStages returned nil")
	}
}

func TestNewStages_ChainedThen(t *testing.T) {
	a := &MockService{name: "a"}
	b := &MockService{name: "b"}
	c := &MockService{name: "c"}
	// Should not panic.
	_ = testrig.NewStages(a).Then(b).Then(c)
}

func TestNewStages_NilServicePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from NewStages(nil)")
		}
		if !strings.Contains(asString(r), "index 0") {
			t.Errorf("Expected panic to mention index 0, got: %v", r)
		}
	}()
	testrig.NewStages(nil)
}

func TestStages_Then_NilServicePanics(t *testing.T) {
	a := &MockService{name: "a"}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Expected panic from Then(nil)")
		}
		if !strings.Contains(asString(r), "index 0") {
			t.Errorf("Expected panic to mention index 0, got: %v", r)
		}
	}()
	testrig.NewStages(a).Then(nil)
}
