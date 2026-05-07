// Package main is a runnable end-to-end demo of integrating testrig with
// a Viper-based application.
//
// The app: a tiny HTTP service that
//  1. reads its config (DB DSN, remote service URL, listen port) via Viper,
//  2. exposes POST /save?key=<k> which fires a background lookup against
//     the configured remote service and persists the response body into
//     Postgres under the given key, and
//  3. exposes GET /lookup?key=<k> for inspection.
//
// The integration: testenv.Setup brings up Postgres, a custom in-process
// schema-seeding service (see seed/), and WireMock through testrig.
// Stages enforce that the seeder runs only after Postgres is up.
//
// Run:
//
//	go run ./examples/viper-app/
//
// Requires Docker (testcontainers).
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/sha1n/testrig/examples/viper-app/config"
	"github.com/sha1n/testrig/examples/viper-app/server"
	"github.com/sha1n/testrig/examples/viper-app/testenv"
)

func main() {
	ctx := context.Background()

	bundle, cleanup, err := testenv.Setup(ctx)
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer cleanup()

	overrides := bundle.Env.Properties()
	overrides["APP_PORT"] = "8080"
	cfg, err := config.Load(overrides)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	db, err := bundle.PG.DB(ctx)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer func() { _ = db.Close() }()

	srv := server.New(cfg, db)
	log.Printf("listening on :%d (DATABASE_URL=%s, REMOTE_URL=%s)",
		cfg.AppPort, cfg.DatabaseURL, cfg.RemoteURL)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.AppPort), srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
