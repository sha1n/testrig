package config_test

import (
	"strings"
	"testing"

	"github.com/sha1n/testrig/examples/koanf-app/config"
)

// clearEnv neutralizes every config key so the test is deterministic even on
// a host that has them set in its environment.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"APP_PORT", "DATABASE_URL", "REMOTE_URL",
		"OIDC_ISSUER_URL", "OIDC_JWKS_URL", "OIDC_AUDIENCE",
	} {
		t.Setenv(k, "")
	}
}

// validOverrides returns a map populating every required field so individual
// tests can omit a single key to exercise its validation branch.
func validOverrides() map[string]string {
	return map[string]string{
		"APP_PORT":        "8080",
		"DATABASE_URL":    "postgres://localhost/x",
		"REMOTE_URL":      "http://localhost:1234",
		"OIDC_ISSUER_URL": "http://localhost:9000",
		"OIDC_JWKS_URL":   "http://localhost:9000/.well-known/jwks.json",
		"OIDC_AUDIENCE":   "example-api",
	}
}

func TestLoad_MissingAppPort(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "APP_PORT")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "APP_PORT") {
		t.Errorf("expected APP_PORT error, got %v", err)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "DATABASE_URL")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoad_MissingRemoteURL(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "REMOTE_URL")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "REMOTE_URL") {
		t.Errorf("expected REMOTE_URL error, got %v", err)
	}
}

func TestLoad_MissingOIDCIssuerURL(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "OIDC_ISSUER_URL")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER_URL") {
		t.Errorf("expected OIDC_ISSUER_URL error, got %v", err)
	}
}

func TestLoad_MissingOIDCJWKSURL(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "OIDC_JWKS_URL")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "OIDC_JWKS_URL") {
		t.Errorf("expected OIDC_JWKS_URL error, got %v", err)
	}
}

func TestLoad_MissingOIDCAudience(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	delete(in, "OIDC_AUDIENCE")
	_, err := config.Load(in)
	if err == nil || !strings.Contains(err.Error(), "OIDC_AUDIENCE") {
		t.Errorf("expected OIDC_AUDIENCE error, got %v", err)
	}
}

func TestLoad_UnmarshalError(t *testing.T) {
	clearEnv(t)
	in := validOverrides()
	in["APP_PORT"] = "not-a-number"
	_, err := config.Load(in)
	if err == nil {
		t.Fatal("expected unmarshal error for non-numeric APP_PORT")
	}
}

func TestLoad_AllValid(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load(validOverrides())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.AppPort != 8080 {
		t.Errorf("AppPort = %d, want 8080", cfg.AppPort)
	}
	if cfg.DatabaseURL != "postgres://localhost/x" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.RemoteURL != "http://localhost:1234" {
		t.Errorf("RemoteURL = %q", cfg.RemoteURL)
	}
	if cfg.OIDCIssuerURL != "http://localhost:9000" {
		t.Errorf("OIDCIssuerURL = %q", cfg.OIDCIssuerURL)
	}
	if cfg.OIDCJWKSURL != "http://localhost:9000/.well-known/jwks.json" {
		t.Errorf("OIDCJWKSURL = %q", cfg.OIDCJWKSURL)
	}
	if cfg.OIDCAudience != "example-api" {
		t.Errorf("OIDCAudience = %q", cfg.OIDCAudience)
	}
}
