// Package testrig — internal tests for zero-value safety of unexported types.
// These tests use package testrig (not testrig_test) so they can access
// unexported types directly.
package testrig

import (
	"context"
	"strings"
	"testing"
)

// internalMockService is a minimal Service implementation for internal tests.
type internalMockService struct{ name string }

func (s *internalMockService) Name() string           { return s.name }
func (s *internalMockService) Identifier() string     { return "mock:" + s.name }
func (s *internalMockService) Dependencies() []string { return nil }
func (s *internalMockService) Start(_ context.Context, _ TestEnvContext) (Properties, error) {
	return nil, nil
}
func (s *internalMockService) Stop(_ context.Context) error { return nil }

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

// --- envDiscovery zero-value ---

func TestEnvDiscovery_ZeroValue_Panics(t *testing.T) {
	d := &envDiscovery{}

	for _, name := range []string{"Discover", "Publish", "Unpublish"} {
		name := name
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("Expected panic from zero-value envDiscovery.%s", name)
				}
				msg, _ := r.(string)
				if !strings.Contains(msg, "NewEnvDiscovery") {
					t.Errorf("Panic message should mention NewEnvDiscovery, got: %v", r)
				}
			}()
			svc := &internalMockService{name: "svc"}
			switch name {
			case "Discover":
				_, _, _ = d.Discover(context.Background(), svc)
			case "Publish":
				_ = d.Publish(context.Background(), svc, Properties{})
			case "Unpublish":
				_ = d.Unpublish(context.Background(), svc)
			}
		})
	}
}
