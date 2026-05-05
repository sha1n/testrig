package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LoadConfig pure-function tests — exercised without spinning up a container so
// the validation branches are covered cheaply.

func TestLoadConfig_MissingAppPort(t *testing.T) {
	_, err := LoadConfig(map[string]string{
		"DATABASE_URL": "postgres://x",
	})
	if err == nil {
		t.Fatal("expected error for missing APP_PORT")
	}
	if !strings.Contains(err.Error(), "APP_PORT") {
		t.Errorf("error should mention APP_PORT; got %q", err.Error())
	}
}

func TestLoadConfig_MissingDatabaseURL(t *testing.T) {
	_, err := LoadConfig(map[string]string{
		"APP_PORT": "8080",
	})
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL; got %q", err.Error())
	}
}

func TestLoadConfig_UnmarshalError(t *testing.T) {
	// APP_PORT must be int; a non-numeric string forces viper.Unmarshal to fail.
	_, err := LoadConfig(map[string]string{
		"APP_PORT":     "not-a-number",
		"DATABASE_URL": "postgres://x",
	})
	if err == nil {
		t.Fatal("expected unmarshal error for non-numeric APP_PORT")
	}
}

func TestHealthHandler(t *testing.T) {
	cfg := &Config{AppPort: 8080, DatabaseURL: "postgres://example"}
	srv := httptest.NewServer(newHandler(cfg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// fakeListener is a minimal net.Listener used to drive run() through serve
// without binding a real port. Accept blocks until Close, so listenAndServe
// can be replaced with an immediate-return stub anyway.
type fakeListener struct{ closed chan struct{} }

func newFakeListener() *fakeListener              { return &fakeListener{closed: make(chan struct{})} }
func (f *fakeListener) Accept() (net.Conn, error) { <-f.closed; return nil, net.ErrClosed }
func (f *fakeListener) Close() error              { close(f.closed); return nil }
func (f *fakeListener) Addr() net.Addr            { return &net.TCPAddr{} }

// withRunHooks installs stubs for run's network/exit indirections, returning
// a teardown func that restores originals.
func withRunHooks(t *testing.T, l func(string, string) (net.Listener, error), s func(net.Listener, http.Handler) error) func() {
	t.Helper()
	origL, origS := listen, listenAndServe
	listen = l
	listenAndServe = s
	return func() { listen = origL; listenAndServe = origS }
}

func TestRun_HappyPath(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://x")

	defer withRunHooks(t,
		func(network, addr string) (net.Listener, error) { return newFakeListener(), nil },
		func(net.Listener, http.Handler) error { return http.ErrServerClosed },
	)()

	if got := run(); got != 0 {
		t.Errorf("run() = %d, want 0", got)
	}
}

func TestRun_ConfigError(t *testing.T) {
	// LoadConfig requires APP_PORT/DATABASE_URL; clear them so it fails.
	t.Setenv("APP_PORT", "")
	t.Setenv("DATABASE_URL", "")

	if got := run(); got != 1 {
		t.Errorf("run() = %d, want 1 (config error)", got)
	}
}

func TestRun_ListenError(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://x")

	defer withRunHooks(t,
		func(string, string) (net.Listener, error) { return nil, net.ErrClosed },
		nil,
	)()

	if got := run(); got != 1 {
		t.Errorf("run() = %d, want 1 (listen error)", got)
	}
}

func TestRun_ServeError(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://x")

	defer withRunHooks(t,
		func(string, string) (net.Listener, error) { return newFakeListener(), nil },
		func(net.Listener, http.Handler) error { return errExpectedServeFailure },
	)()

	if got := run(); got != 1 {
		t.Errorf("run() = %d, want 1 (serve error)", got)
	}
}

var errExpectedServeFailure = errExpected{}

type errExpected struct{}

func (errExpected) Error() string { return "expected serve failure" }

func TestMain_DispatchesToRun(t *testing.T) {
	t.Setenv("APP_PORT", "")
	t.Setenv("DATABASE_URL", "")

	var got int
	origExit := exit
	exit = func(code int) { got = code }
	defer func() { exit = origExit }()

	main()

	if got != 1 {
		t.Errorf("main() exited with %d, want 1 (config error path)", got)
	}
}

func TestViperConfigLoading(t *testing.T) {
	pg := postgres.New("pg").WithDatabase("viper_db")

	env := testrig.MustNew(testrig.With(pg))
	require.NoError(t, env.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, env.Stop(context.Background())) })

	// Parallel-safe: properties are injected via viper.Set (in-memory),
	// not via os.Setenv. Each test gets its own viper instance.
	//
	// The Postgres service exports its DSN under the fixed key "pg.dsn"; the
	// application's config expects "DATABASE_URL". This test bridges from the
	// service's vocabulary to the application's vocabulary at the consumption
	// site — typical pattern when reusing a generic service across apps that
	// each have their own config keys.
	props := env.Properties()
	overrides := map[string]string{
		"DATABASE_URL": props["pg.dsn"],
		"APP_PORT":     "8080",
	}

	cfg, err := LoadConfig(overrides)
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.AppPort)
	assert.Contains(t, cfg.DatabaseURL, "viper_db")
	assert.Contains(t, cfg.DatabaseURL, "postgres://")
}
