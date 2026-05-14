// Package main is a runnable end-to-end demo of integrating testrig with
// a Viper-based application.
//
// The app: a small JWT-protected HTTP API that
//  1. reads its config (DB DSN, remote service URL, OIDC issuer/JWKS URL,
//     audience, listen port) via Viper,
//  2. validates every incoming request's Bearer JWT against the JWKS served
//     by the configured OIDC issuer (RS256, iss, aud, exp) — JWKS resolution
//     via github.com/MicahParks/keyfunc/v3,
//  3. exposes POST /save?key=<k> which fires a background lookup against
//     the configured remote service and persists the response body into
//     Postgres scoped to (sub, key), and
//  4. exposes GET /lookup?key=<k> for inspection, also scoped to (sub, key).
//
// The integration: testenv.Setup brings up Postgres, a custom in-process
// schema-seeding service (see examples/internal/seed), WireMock, and the
// in-process testrig OIDC issuer through testrig. The HTTP wiring and
// graceful-shutdown loop live in package app and are exercised end-to-end
// from app/app_test.go — main here is just an entry point.
//
// Run:
//
//	go run ./examples/viper-app/
//
// Requires Docker (testcontainers). Ctrl-C triggers a graceful shutdown.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/sha1n/testrig/examples/viper-app/app"
	"github.com/sha1n/testrig/examples/viper-app/testenv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bundle, cleanup, err := testenv.Setup(ctx)
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer cleanup()

	db, err := bundle.PG.DB(ctx)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer func() { _ = db.Close() }()

	overrides := bundle.Env.Properties()
	overrides["APP_PORT"] = "8080"

	wired, err := app.New(overrides, db)
	if err != nil {
		log.Fatalf("app: %v", err)
	}

	lis, err := net.Listen("tcp", wired.Addr())
	if err != nil {
		log.Fatalf("listen %s: %v", wired.Addr(), err)
	}
	log.Printf("listening on %s", lis.Addr())
	if err := wired.Run(ctx, lis); err != nil {
		log.Fatal(err)
	}
}
