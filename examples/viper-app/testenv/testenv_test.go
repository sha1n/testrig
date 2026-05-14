package testenv_test

import (
	"context"
	"testing"

	"github.com/sha1n/testrig/examples/viper-app/testenv"
)

// TestSetup_CanceledContext_ReturnsError verifies that an already-canceled
// context causes Setup to fail without bringing services up. This pins
// coverage on the error-return branch of Setup that the happy-path
// integration test cannot reach.
func TestSetup_CanceledContext_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bundle, cleanup, err := testenv.Setup(ctx)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("expected error from Setup with canceled context, got nil")
	}
	if bundle != nil {
		t.Errorf("expected nil bundle on error, got %+v", bundle)
	}
	if cleanup != nil {
		t.Errorf("expected nil cleanup on error, got non-nil")
	}
}
