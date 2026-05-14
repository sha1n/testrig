// Package demo provides startup banners and interactive helpers for the
// example apps. It exists to keep main.go entry points trivial and to share
// the "here's how to hit this API" output across the viper-app and koanf-app
// demos.
package demo

import (
	"fmt"
	"log"
	"time"

	"github.com/sha1n/testrig/services/oidc"
)

// demoSubject is the JWT `sub` claim used for the printed-out token.
const demoSubject = "demo-user"

// demoTokenTTL is the lifetime of the printed-out token.
const demoTokenTTL = time.Hour

// PrintReadyBanner mints a short-lived Bearer token for demoSubject against
// audience and prints a friendly summary plus a ready-to-paste curl command
// that hits GET /lookup on the running app at hostPort (e.g. "127.0.0.1:8080").
// If token minting fails (e.g. issuer not started), the failure is logged and
// the banner is skipped — never fatal, this is a demo affordance.
func PrintReadyBanner(hostPort string, issuer *oidc.Issuer, audience string) {
	token, err := issuer.SignFor(demoSubject, audience, demoTokenTTL)
	if err != nil {
		log.Printf("⚠️  could not mint demo token: %v", err)
		return
	}

	fmt.Println()
	fmt.Println("✅ App is ready!")
	fmt.Printf("   URL:      http://%s\n", hostPort)
	fmt.Printf("   Issuer:   %s\n", issuer.IssuerURL())
	fmt.Printf("   Audience: %s\n", audience)
	fmt.Printf("   Subject:  %s (token valid for %s)\n", demoSubject, demoTokenTTL)
	fmt.Println()
	fmt.Println("👉 Try GET /lookup with a valid Bearer token:")
	fmt.Println()
	fmt.Printf("   curl -s 'http://%s/lookup?key=hello' \\\n", hostPort)
	fmt.Printf("        -H 'Authorization: Bearer %s'\n", token)
	fmt.Println()
	fmt.Println("   (Tip: POST /save?key=hello first to populate via the remote service.)")
	fmt.Println()
}
