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

	"github.com/sha1n/testrig/examples/koanf-app/app"
	"github.com/sha1n/testrig/examples/koanf-app/testenv"
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
