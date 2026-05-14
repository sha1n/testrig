// Package app wires the koanf-app's HTTP server and runs it with graceful
// shutdown. It exists as its own package so that main() stays trivial and
// the wiring (config, JWKS keyfunc, sampleapp.Server) plus the HTTP
// lifecycle are testable as a black box.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/MicahParks/keyfunc/v3"

	"github.com/sha1n/testrig/examples/internal/sampleapp"
	"github.com/sha1n/testrig/examples/koanf-app/config"
)

// shutdownTimeout bounds the in-flight-request drain on graceful shutdown.
const shutdownTimeout = 5 * time.Second

// jwksProbeTimeout bounds the startup reachability check against the JWKS
// endpoint — we want fast-fail on a misconfigured OIDC URL, not a server
// that comes up and 401s every request because keys never materialize.
const jwksProbeTimeout = 3 * time.Second

// App is the wired-up application: a built http.Handler plus the listen
// address derived from config. Construct with New, then either call Run
// (which manages listener + graceful shutdown) or expose Handler directly
// to integration tests.
type App struct {
	addr     string
	handler  http.Handler
	audience string
}

// New loads config from props, resolves the JWKS keyfunc against the OIDC
// issuer, wires the sampleapp HTTP server, and returns a ready-to-run App.
// db is owned by the caller.
func New(props map[string]string, db *sql.DB) (*App, error) {
	cfg, err := config.Load(props)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if err := probeJWKS(cfg.OIDCJWKSURL); err != nil {
		return nil, fmt.Errorf("JWKS endpoint unreachable: %w", err)
	}

	jwks, err := keyfunc.NewDefault([]string{cfg.OIDCJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("build JWKS keyfunc: %w", err)
	}

	srv := sampleapp.New(db, sampleapp.Config{
		RemoteURL: cfg.RemoteURL,
		Auth: sampleapp.AuthConfig{
			KeyFunc:  jwks.Keyfunc,
			Issuer:   cfg.OIDCIssuerURL,
			Audience: cfg.OIDCAudience,
		},
	})

	return &App{
		addr:     fmt.Sprintf(":%d", cfg.AppPort),
		handler:  srv.Handler(),
		audience: cfg.OIDCAudience,
	}, nil
}

// Addr returns the configured listen address (e.g. ":8080").
func (a *App) Addr() string { return a.addr }

// Handler returns the HTTP handler so integration tests can drive it via
// httptest.NewServer without going through Run.
func (a *App) Handler() http.Handler { return a.handler }

// Audience returns the JWT audience the middleware validates against —
// useful for tests minting tokens against the same value.
func (a *App) Audience() string { return a.audience }

// probeJWKS performs a single bounded HTTP GET against jwksURL and returns
// an error if the endpoint isn't reachable or doesn't return 200. The
// MicahParks/keyfunc library fetches asynchronously after construction, so
// without this probe a misconfigured URL would only surface as 401s on
// every request — the probe converts that into a clean startup failure.
func probeJWKS(jwksURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), jwksProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// Run serves on lis until ctx is canceled or the server fails. On ctx.Done,
// it triggers a graceful shutdown bounded by shutdownTimeout. Returns nil
// when the server has stopped cleanly. lis is owned by Run for its lifetime.
func (a *App) Run(ctx context.Context, lis net.Listener) error {
	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(lis)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		sc, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(sc); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}
