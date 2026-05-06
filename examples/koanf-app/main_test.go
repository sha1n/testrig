package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupEnv exercises the same integration that main() runs: spin up a
// testrig.Env containing a Postgres service, feed env.Properties() into koanf,
// and confirm the resulting Config points at the live container.
//
// This is the canonical "test the integration" surface for the example.
func TestSetupEnv(t *testing.T) {
	cfg, cleanup, err := setupEnv(context.Background())
	require.NoError(t, err)
	t.Cleanup(cleanup)

	assert.Equal(t, 9090, cfg.AppPort)
	assert.Contains(t, cfg.DatabaseURL, "koanf_db")
	assert.Contains(t, cfg.DatabaseURL, "postgres://")
}

// TestSetupEnv_HandlerEndToEnd goes one step further: takes the Config that
// setupEnv produces, instantiates the actual handler the app would serve, and
// hits /health to confirm the live DSN flows all the way through.
func TestSetupEnv_HandlerEndToEnd(t *testing.T) {
	cfg, cleanup, err := setupEnv(context.Background())
	require.NoError(t, err)
	t.Cleanup(cleanup)

	srv := httptest.NewServer(newHandler(cfg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "koanf_db")
	assert.Contains(t, string(body), "postgres://")
}

// --- loadConfig validation paths (no container) ---

func TestLoadConfig_MissingAppPort(t *testing.T) {
	_, err := loadConfig(map[string]string{"DATABASE_URL": "postgres://x"})
	if err == nil || !strings.Contains(err.Error(), "APP_PORT") {
		t.Errorf("expected APP_PORT error, got %v", err)
	}
}

func TestLoadConfig_MissingDatabaseURL(t *testing.T) {
	_, err := loadConfig(map[string]string{"APP_PORT": "9090"})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadConfig_UnmarshalError(t *testing.T) {
	_, err := loadConfig(map[string]string{
		"APP_PORT":     "not-a-number",
		"DATABASE_URL": "postgres://x",
	})
	if err == nil {
		t.Fatal("expected unmarshal error for non-numeric APP_PORT")
	}
}
