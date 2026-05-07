package server_test

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wiremock/go-wiremock"

	"github.com/sha1n/testrig/examples/viper-app/config"
	"github.com/sha1n/testrig/examples/viper-app/server"
	"github.com/sha1n/testrig/examples/viper-app/testenv"
)

// Shared across all tests in this package — set up once in TestMain.
var (
	bundle  *testenv.Bundle
	db      *sql.DB
	handler http.Handler
)

// TestMain spins up the test environment exactly once for the package,
// runs all tests against the shared resources, then tears down.
func TestMain(m *testing.M) {
	ctx := context.Background()

	b, cleanup, err := testenv.Setup(ctx)
	if err != nil {
		log.Fatalf("testenv.Setup: %v", err)
	}
	bundle = b

	overrides := bundle.Env.Properties()
	overrides["APP_PORT"] = "9999" // arbitrary; tests use httptest with auto-assigned ports
	cfg, err := config.Load(overrides)
	if err != nil {
		cleanup()
		log.Fatalf("config.Load: %v", err)
	}

	db, err = bundle.PG.DB(ctx)
	if err != nil {
		cleanup()
		log.Fatalf("pg.DB: %v", err)
	}

	handler = server.New(cfg, db).Handler()

	code := m.Run()

	_ = db.Close()
	cleanup()
	os.Exit(code)
}

// resetWireMock removes all stubs from WireMock so each test starts
// from a clean slate. Called explicitly by tests that stub the remote.
func resetWireMock(t *testing.T) {
	t.Helper()
	if err := bundle.WM.Client().Reset(); err != nil {
		t.Fatalf("reset wiremock: %v", err)
	}
}

// TestSaveEndpoint_PersistsRemoteResponse: end-to-end happy path.
func TestSaveEndpoint_PersistsRemoteResponse(t *testing.T) {
	resetWireMock(t)

	const key = "alpha"
	const expectedBody = `{"data":"alpha-value"}`

	require.NoError(t, bundle.WM.Client().StubFor(
		wiremock.Get(wiremock.URLPathEqualTo("/lookup")).
			WithQueryParam("key", wiremock.EqualTo(key)).
			WillReturnResponse(wiremock.NewResponse().
				WithStatus(http.StatusOK).
				WithHeaders(map[string]string{"Content-Type": "application/json"}).
				WithBody(expectedBody)),
	))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/save?key="+key, "", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Long timeout, very frequent polling: happy path returns in tens of
	// ms; the 10s ceiling only fires on a real failure, so the test
	// can't flake on a loaded CI runner.
	require.Eventually(t, func() bool {
		var got string
		err := db.QueryRow(`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
		return err == nil && got == expectedBody
	}, 10*time.Second, 10*time.Millisecond, "DB row for key=%q did not appear in time", key)

	got, err := http.Get(srv.URL + "/lookup?key=" + key)
	require.NoError(t, err)
	defer func() { _ = got.Body.Close() }()
	body, _ := io.ReadAll(got.Body)
	assert.Equal(t, http.StatusOK, got.StatusCode)
	assert.Equal(t, expectedBody, string(body))
}

// TestSaveEndpoint_MissingKey_Returns400 verifies validation.
func TestSaveEndpoint_MissingKey_Returns400(t *testing.T) {
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/save", "", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestLookupEndpoint_UnknownKey_Returns404 verifies the not-found path.
// Uses a unique key per test to avoid collision with TestSaveEndpoint.
func TestLookupEndpoint_UnknownKey_Returns404(t *testing.T) {
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/lookup?key=unknown-" + t.Name())
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestSchemaSeed_AppliedDuringSetup: the SchemaSeed service ran and
// published its applied marker. Demonstrates the custom non-dockerized
// service is participating in the startup sequence.
func TestSchemaSeed_AppliedDuringSetup(t *testing.T) {
	props := bundle.Env.Properties()
	if props["seed.applied"] != "true" {
		t.Errorf("expected seed.applied=true, got %q", props["seed.applied"])
	}
}
