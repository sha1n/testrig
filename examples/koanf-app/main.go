// Package main is a runnable end-to-end demo of integrating testrig with
// a koanf-based application.
//
// Layout mirrors the viper-app: thin entry point that delegates to
// testenv.Setup, config.Load, and sampleapp.New. See examples/viper-app
// for a doc-comment-rich version.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/sha1n/testrig/examples/internal/sampleapp"
	"github.com/sha1n/testrig/examples/koanf-app/config"
	"github.com/sha1n/testrig/examples/koanf-app/testenv"
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

	srv := sampleapp.New(db, cfg.RemoteURL)
	log.Printf("listening on :%d (DATABASE_URL=%s, REMOTE_URL=%s)",
		cfg.AppPort, cfg.DatabaseURL, cfg.RemoteURL)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.AppPort), srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
