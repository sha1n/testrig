package oidc_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/testrig/services/oidc"
)

func TestStart_MinimalConfig_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/cb").
		WithAllowedAudiences("api")
	props, err := iss.Start(context.Background(), slog.Default())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
	if iss.IssuerURL() == "" {
		t.Errorf("IssuerURL is empty after Start")
	}
	if props == nil {
		t.Errorf("Start returned nil properties")
	}
}

func TestStart_FullConfig_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithKeyID("kid-1").
		WithClientID("client-abc").
		WithClientSecret("secret-xyz").
		WithRedirectURIs("http://localhost:8080/cb", "http://localhost:9090/cb").
		WithAllowedAudiences("api-1", "api-2").
		WithDefaultSubject("alice").
		WithTokenTTL(2 * time.Minute).
		WithIssuerURLPropertyName("ISSUER").
		WithJWKSURLPropertyName("JWKS").
		WithDiscoveryURLPropertyName("DISCOVERY").
		WithClientIDPropertyName("CLIENT_ID").
		WithClientSecretPropertyName("CLIENT_SECRET").
		WithAudiencePropertyName("AUDIENCE")
	if _, err := iss.Start(context.Background(), slog.Default()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
}

func TestStart_StartTwice_ReturnsError(t *testing.T) {
	iss := startMinimal(t)
	_, err := iss.Start(context.Background(), slog.Default())
	if err == nil {
		t.Fatal("expected error on second Start, got nil")
	}
	if !strings.Contains(err.Error(), "already started") {
		t.Errorf("expected error to mention 'already started', got %v", err)
	}
}

func TestStop_StopThenStart_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/cb").
		WithAllowedAudiences("api")
	if _, err := iss.Start(context.Background(), slog.Default()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := iss.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := iss.Start(context.Background(), slog.Default()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
	if iss.IssuerURL() == "" {
		t.Error("IssuerURL empty after restart")
	}
}

func TestStop_BeforeStart_NoOp(t *testing.T) {
	iss := oidc.New("idp")
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("Stop before Start should be no-op, got %v", err)
	}
}

func TestStop_Twice_NoOp(t *testing.T) {
	iss := startMinimal(t)
	// Stop is called twice here; t.Cleanup from startMinimal will call it
	// a third time. All three calls should succeed without error.
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("first explicit Stop: %v", err)
	}
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("second Stop should be no-op, got %v", err)
	}
}
