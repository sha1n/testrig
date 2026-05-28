// Package demo provides startup banners and interactive helpers for the
// example apps. It exists to keep main.go entry points trivial and to share
// the "here's how to hit this API" output across the viper-app and koanf-app
// demos.
package demo

import (
	"fmt"
	"log"
	"net/http"
	"time"

	wiremock "github.com/wiremock/go-wiremock"

	"github.com/sha1n/testrig/oidc"
)

const (
	// demoSubject is the JWT `sub` claim used for the printed-out token.
	demoSubject = "demo-user"
	// demoTokenTTL is the lifetime of the printed-out token.
	demoTokenTTL = time.Hour
	// demoKey is the key the printed curl commands operate on.
	demoKey = "hello"
	// demoRemoteBody is the body WireMock returns for the demo lookup. POST
	// /save fetches and persists this; GET /lookup echoes it back.
	demoRemoteBody = `{"message":"hello from the remote service"}`
)

// PrintReadyBanner makes the demo flow actually work end-to-end:
//   - stubs the WireMock remote so POST /save?key=hello has something to fetch,
//   - mints a short-lived Bearer token for demoSubject against audience,
//   - prints copy-pasteable POST /save and GET /lookup curl commands targeting
//     the running app at hostPort (e.g. "127.0.0.1:8080").
//
// Token-mint or stub failures are logged but never fatal — this is a demo
// affordance, not a startup precondition.
func PrintReadyBanner(hostPort string, issuer *oidc.Issuer, audience string, wm *wiremock.Client) {
	token, err := issuer.SignFor(demoSubject, audience, demoTokenTTL)
	if err != nil {
		log.Printf("⚠️  could not mint demo token: %v", err)
		return
	}

	if err := stubDemoLookup(wm); err != nil {
		log.Printf("⚠️  could not stub remote lookup (POST /save will get a 404 body): %v", err)
	}

	fmt.Println()
	fmt.Println("✅ App is ready!")
	fmt.Printf("   URL:      http://%s\n", hostPort)
	fmt.Printf("   Issuer:   %s\n", issuer.IssuerURL())
	fmt.Printf("   Audience: %s\n", audience)
	fmt.Printf("   Subject:  %s (token valid for %s)\n", demoSubject, demoTokenTTL)
	fmt.Println()
	fmt.Println("👉 Step 1 — POST /save fetches the demo body from the remote and persists it:")
	fmt.Println()
	fmt.Printf("   curl -i -X POST 'http://%s/save?key=%s' \\\n", hostPort, demoKey)
	fmt.Printf("        -H 'Authorization: Bearer %s'\n", token)
	fmt.Println()
	fmt.Println("👉 Step 2 — GET /lookup returns the persisted body:")
	fmt.Println()
	fmt.Printf("   curl -s 'http://%s/lookup?key=%s' \\\n", hostPort, demoKey)
	fmt.Printf("        -H 'Authorization: Bearer %s'\n", token)
	fmt.Println()
}

// stubDemoLookup configures WireMock to return demoRemoteBody for the
// GET /lookup?key=<demoKey> the sampleapp fires from POST /save.
func stubDemoLookup(wm *wiremock.Client) error {
	return wm.StubFor(
		wiremock.Get(wiremock.URLPathEqualTo("/lookup")).
			WithQueryParam("key", wiremock.EqualTo(demoKey)).
			WillReturnResponse(wiremock.NewResponse().
				WithStatus(http.StatusOK).
				WithHeaders(map[string]string{"Content-Type": "application/json"}).
				WithBody(demoRemoteBody)),
	)
}
