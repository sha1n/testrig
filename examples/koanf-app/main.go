// Package main is a runnable end-to-end demo of integrating testrig with
// a koanf-based application. Layout mirrors the viper-app: thin entry point
// that delegates to testenv.Setup and app.New + app.Run.
//
// See examples/viper-app for a doc-comment-rich version.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/sha1n/testrig/examples/internal/demo"
	"github.com/sha1n/testrig/examples/koanf-app/app"
	"github.com/sha1n/testrig/examples/koanf-app/testenv"
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

	log.Println("🚀 Bringing up the koanf-app test environment...")
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

	demo.PrintReadyBanner(listenHost+":"+appPort, bundle.Issuer, wired.Audience(), bundle.WM.Client())

	if err := wired.Run(ctx, lis); err != nil {
		log.Fatalf("❌ server stopped with error: %v", err)
	}
	log.Println("👋 Bye!")
}
