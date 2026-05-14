package app_test

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sha1n/testrig/examples/internal/sampletest"
	"github.com/sha1n/testrig/examples/koanf-app/app"
	"github.com/sha1n/testrig/examples/koanf-app/testenv"
)

// Shared across all tests in this package — set up once in TestMain.
var (
	bundle  *testenv.Bundle
	db      *sql.DB
	wired   *app.App
	harness *sampletest.Harness
)

// TestMain spins up the test environment exactly once for the package,
// builds the koanf-loaded App via app.New, and constructs the harness used
// by the named scenario delegators.
func TestMain(m *testing.M) {
	ctx := context.Background()

	b, cleanup, err := testenv.Setup(ctx)
	if err != nil {
		log.Fatalf("testenv.Setup: %v", err)
	}
	bundle = b

	db, err = bundle.PG.DB(ctx)
	if err != nil {
		cleanup()
		log.Fatalf("pg.DB: %v", err)
	}

	overrides := bundle.Env.Properties()
	overrides["APP_PORT"] = "9999" // arbitrary; tests don't use this port

	wired, err = app.New(overrides, db)
	if err != nil {
		_ = db.Close()
		cleanup()
		log.Fatalf("app.New: %v", err)
	}

	harness = sampletest.New(db, wired.Handler(), bundle.Issuer, bundle.WM.Client(), wired.Audience())

	code := m.Run()

	_ = db.Close()
	cleanup()
	os.Exit(code)
}

func TestSaveAndLookup_AuthenticatedHappyPath(t *testing.T) {
	harness.SaveAndLookupHappyPath(t)
}

func TestSave_MissingToken_Returns401(t *testing.T) {
	harness.MissingToken(t)
}

func TestSave_ExpiredToken_Returns401(t *testing.T) {
	harness.ExpiredToken(t)
}

func TestSave_WrongAudience_Returns401(t *testing.T) {
	harness.WrongAudience(t)
}

func TestSave_BadSignature_Returns401(t *testing.T) {
	harness.BadSignature(t)
}

func TestLookup_PerUserIsolation(t *testing.T) {
	harness.PerUserIsolation(t)
}

func TestSchemaSeed_AppliedDuringSetup(t *testing.T) {
	harness.SchemaSeedApplied(t, bundle.Env.Properties())
}

// propsCopy returns a fresh copy of the env's properties so tests can mutate
// it without affecting other tests, and clears the matching OS env vars so
// the test outcome doesn't depend on the host's environment.
func propsCopy(t *testing.T) map[string]string {
	t.Helper()
	src := bundle.Env.Properties()
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
		t.Setenv(k, "")
	}
	out["APP_PORT"] = "9999"
	return out
}

// TestNew_BadJWKSURL_ReturnsError exercises the JWKS-keyfunc-build error
// path in app.New. A URL that points nowhere fails fast at startup, which
// is exactly what a real ops smoke test would catch on a misconfigured
// deployment.
func TestNew_BadJWKSURL_ReturnsError(t *testing.T) {
	overrides := propsCopy(t)
	overrides["OIDC_JWKS_URL"] = "http://127.0.0.1:1/does-not-exist"

	_, err := app.New(overrides, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWKS")
}

// TestNew_BadConfig_ReturnsError exercises the config-load error path in
// app.New — missing a required field causes a clear startup failure.
func TestNew_BadConfig_ReturnsError(t *testing.T) {
	overrides := propsCopy(t)
	overrides["OIDC_ISSUER_URL"] = ""

	_, err := app.New(overrides, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config")
}

// TestRun_ServesAndShutsDownGracefully verifies the production-path Run:
// bring up the server on a kernel-assigned port, make a real request against
// it, cancel the context, and assert that Run returns cleanly. Mirrors what
// an ops smoke-test does.
func TestRun_ServesAndShutsDownGracefully(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- wired.Run(ctx, lis) }()

	url := "http://" + lis.Addr().String() + "/save"
	tok := harness.TokenFor(t, harness.UserAlice)

	// /save without a key returns 400 — confirms Run is serving and the
	// middleware is wired in.
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err, "Run returned non-nil after graceful shutdown")
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of ctx cancel")
	}

	// After shutdown the listener is closed; further connects fail.
	_, dialErr := net.DialTimeout("tcp", lis.Addr().String(), 200*time.Millisecond)
	assert.Error(t, dialErr, "expected dial failure after shutdown")
}
