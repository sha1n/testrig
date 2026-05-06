package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wiremock/go-wiremock"
)

// TestSaveEndpoint_PersistsRemoteResponse is the end-to-end integration test:
//
//  1. setupEnv brings up Postgres + WireMock through testrig and wires the app.
//  2. The test stubs WireMock's /lookup so a known response is returned.
//  3. The test calls the app's POST /save?key=alpha endpoint. The handler
//     enqueues an async fetchAndStore.
//  4. The test polls the DB until a row with the expected body appears, with
//     a timeout — modelling how a caller would observe asynchronous work
//     completing.
//
// This is the canonical "test the integration" surface for the example.
func TestSaveEndpoint_PersistsRemoteResponse(t *testing.T) {
	s, cleanup, err := setupEnv(context.Background())
	require.NoError(t, err)
	t.Cleanup(cleanup)

	const expectedBody = `{"data":"alpha-value"}`
	require.NoError(t, s.WireMock.Client().StubFor(
		wiremock.Get(wiremock.URLPathEqualTo("/lookup")).
			WithQueryParam("key", wiremock.EqualTo("alpha")).
			WillReturnResponse(wiremock.NewResponse().
				WithStatus(http.StatusOK).
				WithHeaders(map[string]string{"Content-Type": "application/json"}).
				WithBody(expectedBody)),
	))

	srv := httptest.NewServer(s.Handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/save?key=alpha", "", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	require.Eventually(t, func() bool {
		var got string
		err := s.DB.QueryRow(`SELECT response FROM responses WHERE key = $1`, "alpha").Scan(&got)
		return err == nil && got == expectedBody
	}, 5*time.Second, 50*time.Millisecond, "DB row for key=alpha did not contain expected body within timeout")

	got, err := http.Get(srv.URL + "/lookup?key=alpha")
	require.NoError(t, err)
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	assert.Equal(t, http.StatusOK, got.StatusCode)
	assert.Equal(t, expectedBody, string(body))
}

func TestSaveEndpoint_MissingKey_Returns400(t *testing.T) {
	s, cleanup, err := setupEnv(context.Background())
	require.NoError(t, err)
	t.Cleanup(cleanup)

	srv := httptest.NewServer(s.Handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/save", "", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestLookupEndpoint_UnknownKey_Returns404(t *testing.T) {
	s, cleanup, err := setupEnv(context.Background())
	require.NoError(t, err)
	t.Cleanup(cleanup)

	srv := httptest.NewServer(s.Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/lookup?key=nope")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- loadConfig validation paths (no container) ---

func TestLoadConfig_MissingAppPort(t *testing.T) {
	_, err := loadConfig(map[string]string{
		"DATABASE_URL": "postgres://x",
		"REMOTE_URL":   "http://x",
	})
	if err == nil || !strings.Contains(err.Error(), "APP_PORT") {
		t.Errorf("expected APP_PORT error, got %v", err)
	}
}

func TestLoadConfig_MissingDatabaseURL(t *testing.T) {
	_, err := loadConfig(map[string]string{
		"APP_PORT":   "9090",
		"REMOTE_URL": "http://x",
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadConfig_MissingRemoteURL(t *testing.T) {
	_, err := loadConfig(map[string]string{
		"APP_PORT":     "9090",
		"DATABASE_URL": "postgres://x",
	})
	if err == nil || !strings.Contains(err.Error(), "REMOTE_URL") {
		t.Errorf("expected REMOTE_URL error, got %v", err)
	}
}

func TestLoadConfig_UnmarshalError(t *testing.T) {
	_, err := loadConfig(map[string]string{
		"APP_PORT":     "not-a-number",
		"DATABASE_URL": "postgres://x",
		"REMOTE_URL":   "http://x",
	})
	if err == nil {
		t.Fatal("expected unmarshal error for non-numeric APP_PORT")
	}
}
