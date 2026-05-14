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

	"github.com/sha1n/testrig/examples/internal/demo"
	"github.com/sha1n/testrig/examples/viper-app/app"
	"github.com/sha1n/testrig/examples/viper-app/testenv"
)

// listenHost is the loopback address the demo binds to and prints in the
// ready-banner curl example. APP_PORT controls the port (set below).
const listenHost = "127.0.0.1"

// appPort is the port the demo listens on. Kept simple so the printed curl
// command is copy-paste friendly.
const appPort = "8080"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("🚀 Bringing up the viper-app test environment...")
	bundle, cleanup, err := testenv.Setup(ctx)
	if err != nil {
		log.Fatalf("❌ test environment setup failed: %v", err)
	}
	defer cleanup()
	log.Println("✅ Test environment is up")

	db, err := bundle.PG.DB(ctx)
	if err != nil {
		log.Fatalf("❌ could not open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	overrides := bundle.Env.Properties()
	overrides["APP_PORT"] = appPort

	log.Println("🔧 Wiring the application...")
	wired, err := app.New(overrides, db)
	if err != nil {
		log.Fatalf("❌ application wiring failed: %v", err)
	}

	lis, err := net.Listen("tcp", wired.Addr())
	if err != nil {
		log.Fatalf("❌ could not listen on %s: %v", wired.Addr(), err)
	}
	log.Printf("🌐 HTTP server listening on %s", lis.Addr())

	demo.PrintReadyBanner(listenHost+":"+appPort, bundle.Issuer, wired.Audience())

	if err := wired.Run(ctx, lis); err != nil {
		log.Fatalf("❌ server stopped with error: %v", err)
	}
	log.Println("👋 Bye!")
}
