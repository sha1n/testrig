// Package main is a runnable end-to-end demo of integrating testrig with a
// Viper-based application.
//
// The app: a tiny HTTP service that
//  1. reads its config (DB DSN, remote service URL, listen port) via Viper,
//  2. exposes POST /save?key=<k> which fires a background lookup against the
//     configured remote service and persists the response body into Postgres
//     under the given key, and
//  3. exposes GET /lookup?key=<k> for inspection.
//
// The integration: setupEnv builds a testrig.Env that brings up real Postgres
// and WireMock containers, layers env.Properties() onto Viper as overrides,
// runs the schema migration, and returns the wired-up pieces. Both main and
// the test use setupEnv, so the demo and the test exercise the same code.
//
// Run:
//
//	go run ./examples/viper-app/
//
// (The remote /lookup target won't have stubs, so /save will record an
// "unmatched" response from WireMock. The test sets up real stubs.)
//
// Requires Docker (testcontainers).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/postgres"
	"github.com/sha1n/testrig/services/wiremock"
	"github.com/spf13/viper"
)

// Config is the typed application config. Production code reads it the same
// way demo code does — Viper handles the source.
type Config struct {
	AppPort     int    `mapstructure:"APP_PORT"`
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	RemoteURL   string `mapstructure:"REMOTE_URL"`
}

// loadConfig builds a Config using Viper. Overrides are applied via v.Set,
// which is Viper's highest-precedence layer (above env vars, files, defaults).
// Production code passes nil; the testrig integration passes env.Properties().
func loadConfig(overrides map[string]string) (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	bindEnvs(v, Config{})

	for k, val := range overrides {
		v.Set(k, val)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	if cfg.AppPort == 0 {
		return nil, fmt.Errorf("APP_PORT is required")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RemoteURL == "" {
		return nil, fmt.Errorf("REMOTE_URL is required")
	}
	return &cfg, nil
}

func bindEnvs(v *viper.Viper, iface any) {
	t := reflect.TypeOf(iface)
	for i := 0; i < t.NumField(); i++ {
		envName := t.Field(i).Tag.Get("mapstructure")
		if envName != "" {
			_ = v.BindEnv(envName)
		}
	}
}

// schemaDDL is the table the demo app stores remote-lookup responses in.
// Applied on startup via migrate.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS responses (
    key        TEXT PRIMARY KEY,
    response   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}
	return nil
}

// fetchAndStore looks up `key` against the remote service and persists the
// response body into the DB under that key. Used as the async worker behind
// POST /save.
func fetchAndStore(ctx context.Context, remoteURL string, db *sql.DB, key string) error {
	target := remoteURL + "/lookup?key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read remote body: %w", err)
	}
	_, err = db.ExecContext(ctx, `
        INSERT INTO responses (key, response) VALUES ($1, $2)
        ON CONFLICT (key) DO UPDATE SET response = EXCLUDED.response, created_at = now()
    `, key, string(body))
	if err != nil {
		return fmt.Errorf("insert response for key=%s: %w", key, err)
	}
	return nil
}

// newHandler builds the HTTP handler for the demo app.
//   - POST /save?key=<k> queues an async fetchAndStore and returns 202.
//   - GET  /lookup?key=<k> returns the stored response (404 if missing).
func newHandler(cfg *Config, db *sql.DB) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /save", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "missing key", http.StatusBadRequest)
			return
		}
		// Fire-and-forget: the test polls the DB to observe completion.
		// A separate context lets the work outlive the request.
		go func() {
			if err := fetchAndStore(context.Background(), cfg.RemoteURL, db, key); err != nil {
				log.Printf("fetchAndStore key=%s: %v", key, err)
			}
		}()
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("GET /lookup", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "missing key", http.StatusBadRequest)
			return
		}
		var body string
		err := db.QueryRowContext(r.Context(), `SELECT response FROM responses WHERE key = $1`, key).Scan(&body)
		switch {
		case err == sql.ErrNoRows:
			http.NotFound(w, r)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}
	})
	return mux
}

// Setup is the result of setupEnv: the resolved config, an open DB handle,
// the wired-up HTTP handler, and the WireMock service handle (so tests can
// install stubs against the remote). Cleanup tears down DB + env.
type Setup struct {
	Cfg      *Config
	DB       *sql.DB
	Handler  http.Handler
	WireMock *wiremock.WireMock
}

// setupEnv brings up Postgres + WireMock via testrig, builds the application
// Config from env.Properties(), runs the schema migration, and returns the
// wired-up Setup. This is the single integration surface — main and the
// test below both call it.
func setupEnv(ctx context.Context) (*Setup, func(), error) {
	pg := postgres.New("pg").
		WithDatabase("viper_app").
		WithDSNPropertyName("DATABASE_URL")
	wm := wiremock.New("wm").
		WithURLPropertyName("REMOTE_URL")

	env := testrig.MustNew(testrig.With(pg, wm))
	if err := env.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("env.Start: %w", err)
	}

	cleanup := func() { _ = env.Stop(context.Background()) }

	port, err := freePort()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("freePort: %w", err)
	}
	overrides := env.Properties()
	overrides["APP_PORT"] = strconv.Itoa(port)

	cfg, err := loadConfig(overrides)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("loadConfig: %w", err)
	}

	db, err := pg.DB(ctx)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("pg.DB: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		cleanup()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	fullCleanup := func() {
		_ = db.Close()
		cleanup()
	}

	return &Setup{
		Cfg:      cfg,
		DB:       db,
		Handler:  newHandler(cfg, db),
		WireMock: wm,
	}, fullCleanup, nil
}

// freePort asks the OS for an unused TCP port and returns the number after
// releasing it. There is a small TOCTOU window between Close and the caller
// rebinding the port, but that is acceptable for the demo and irrelevant for
// the test (which uses httptest.NewServer with its own auto-assigned port).
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func main() {
	ctx := context.Background()
	s, cleanup, err := setupEnv(ctx)
	if err != nil {
		log.Fatalf("setup error: %v", err)
	}
	defer cleanup()

	log.Printf("testrig env up; serving on :%d (DATABASE_URL=%s, REMOTE_URL=%s)", s.Cfg.AppPort, s.Cfg.DatabaseURL, s.Cfg.RemoteURL)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", s.Cfg.AppPort), s.Handler); err != nil {
		log.Fatal(err)
	}
}
