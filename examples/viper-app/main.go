// Package main is a runnable end-to-end demo of integrating testrig with a
// Viper-based application.
//
// The app: a tiny HTTP service that reads APP_PORT and DATABASE_URL from
// Viper and serves /health. The config-loading code is the kind a production
// service would have — no testrig awareness.
//
// The integration: setupEnv builds a testrig.Env that brings up a real
// Postgres container, then layers env.Properties() onto Viper as overrides
// (Viper's highest-precedence layer). Both main and main_test use the same
// setupEnv so the demo and the test exercise the same code path.
//
// Run:
//
//	go run ./examples/viper-app/
//
// Then `curl http://localhost:8080/health` returns the live container DSN.
// Requires Docker (testcontainers).
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"reflect"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/postgres"
	"github.com/spf13/viper"
)

// Config is the application's typed config. Production code reads it the same
// way demo code does — Viper handles the source.
type Config struct {
	AppPort     int    `mapstructure:"APP_PORT"`
	DatabaseURL string `mapstructure:"DATABASE_URL"`
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

func newHandler(cfg *Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK - " + cfg.DatabaseURL))
	})
	return mux
}

// setupEnv wires testrig + Viper together: brings up a Postgres service whose
// DSN is published directly under DATABASE_URL (the application's config key),
// adds the application-only APP_PORT key, and feeds the merged map into Viper
// to produce a Config. Returns the Config and a cleanup function that stops
// the env.
//
// This is the heart of the integration — both main and the test below call it.
func setupEnv(ctx context.Context) (*Config, func(), error) {
	env := testrig.MustNew(testrig.With(
		postgres.New("pg").
			WithDatabase("viper_db").
			WithDSNPropertyName("DATABASE_URL"),
	))
	if err := env.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("env.Start: %w", err)
	}

	overrides := env.Properties()
	overrides["APP_PORT"] = "8080"

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
