// Package main is a runnable end-to-end demo of integrating testrig with a
// koanf-based application.
//
// The app: a tiny HTTP service that reads APP_PORT and DATABASE_URL via
// koanf and serves /health. The config-loading code is the kind a production
// service would have — no testrig awareness.
//
// The integration: setupEnv builds a testrig.Env that brings up a real
// Postgres container, then layers env.Properties() onto koanf via the
// confmap provider (which sits above env vars in precedence). Both main and
// main_test use the same setupEnv so the demo and the test exercise the same
// code path.
//
// Run:
//
//	go run ./examples/koanf-app/
//
// Then `curl http://localhost:9090/health` returns the live container DSN.
// Requires Docker (testcontainers).
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/postgres"
)

// Config is the application's typed config. Production code reads it the same
// way demo code does — koanf handles the source.
type Config struct {
	AppPort     int    `koanf:"app_port"`
	DatabaseURL string `koanf:"database_url"`
}

// loadConfig builds a Config using koanf. Environment variables are loaded
// first; overrides layer on top via confmap.Provider so they win.
// Production code passes nil; the testrig integration passes env.Properties().
func loadConfig(overrides map[string]string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(env.Provider("", ".", func(s string) string {
		return strings.ToLower(s)
	}), nil); err != nil {
		return nil, fmt.Errorf("error loading env: %w", err)
	}

	if len(overrides) > 0 {
		m := make(map[string]any, len(overrides))
		for key, val := range overrides {
			m[strings.ToLower(key)] = val
		}
		if err := k.Load(confmap.Provider(m, "."), nil); err != nil {
			return nil, fmt.Errorf("error loading overrides: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling: %w", err)
	}

	if cfg.AppPort == 0 {
		return nil, fmt.Errorf("APP_PORT is required")
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	return &cfg, nil
}

func newHandler(cfg *Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK - " + cfg.DatabaseURL))
	})
	return mux
}

// setupEnv wires testrig + koanf together: brings up a Postgres service whose
// DSN is published directly under DATABASE_URL (the application's config key),
// adds the application-only APP_PORT key, and feeds the merged map into koanf
// to produce a Config. Returns the Config and a cleanup function that stops
// the env.
//
// Publishing under a flat key like DATABASE_URL (rather than the service's
// default "pg.dsn") matters extra here because koanf treats "." as a delimiter
// — a default key would create a nested map rather than a flat config key.
//
// This is the heart of the integration — both main and the test below call it.
func setupEnv(ctx context.Context) (*Config, func(), error) {
	env := testrig.MustNew(testrig.With(
		postgres.New("pg").
			WithDatabase("koanf_db").
			WithDSNPropertyName("DATABASE_URL"),
	))
	if err := env.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("env.Start: %w", err)
	}

	overrides := env.Properties()
	overrides["APP_PORT"] = "9090"

	cfg, err := loadConfig(overrides)
	if err != nil {
		_ = env.Stop(ctx)
		return nil, nil, fmt.Errorf("loadConfig: %w", err)
	}

	cleanup := func() { _ = env.Stop(context.Background()) }
	return cfg, cleanup, nil
}

func main() {
	ctx := context.Background()

	cfg, cleanup, err := setupEnv(ctx)
	if err != nil {
		log.Fatalf("setup error: %v", err)
	}
	defer cleanup()

	log.Printf("testrig env up; serving on :%d with DATABASE_URL=%s", cfg.AppPort, cfg.DatabaseURL)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.AppPort), newHandler(cfg)); err != nil {
		log.Fatal(err)
	}
}
