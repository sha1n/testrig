package config_test

import (
	"strings"
	"testing"

	"github.com/sha1n/testrig/examples/viper-app/config"
)

// clearEnv neutralizes the three config keys so the test is deterministic
// even on a host that has them set in its environment. t.Setenv reverts
// on test cleanup.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REMOTE_URL", "")
}

func TestLoad_MissingAppPort(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(map[string]string{
		"DATABASE_URL": "postgres://x",
		"REMOTE_URL":   "http://x",
	})
	if err == nil || !strings.Contains(err.Error(), "APP_PORT") {
		t.Errorf("expected APP_PORT error, got %v", err)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(map[string]string{
		"APP_PORT":   "8080",
		"REMOTE_URL": "http://x",
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoad_MissingRemoteURL(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(map[string]string{
		"APP_PORT":     "8080",
		"DATABASE_URL": "postgres://x",
	})
	if err == nil || !strings.Contains(err.Error(), "REMOTE_URL") {
		t.Errorf("expected REMOTE_URL error, got %v", err)
	}
}

func TestLoad_UnmarshalError(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(map[string]string{
		"APP_PORT":     "not-a-number",
		"DATABASE_URL": "postgres://x",
		"REMOTE_URL":   "http://x",
	})
	if err == nil {
		t.Fatal("expected unmarshal error for non-numeric APP_PORT")
	}
}

func TestLoad_AllValid(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load(map[string]string{
		"APP_PORT":     "8080",
		"DATABASE_URL": "postgres://localhost/x",
		"REMOTE_URL":   "http://localhost:1234",
	})
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
}
