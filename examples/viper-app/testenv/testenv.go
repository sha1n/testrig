// Package testenv wires the viper-app's test environment via testrig.
// Setup is the single integration surface — both main and the server
// integration tests call it.
package testenv

import (
	"context"
	"fmt"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/examples/internal/seed"
	"github.com/sha1n/testrig/oidc"
	"github.com/sha1n/testrig/postgres"
	"github.com/sha1n/testrig/wiremock"
)

// Audience is the OAuth audience the OIDC issuer accepts and the app's
// middleware validates against. Exposed so tests can mint tokens for it.
const Audience = "example-api"

// Bundle is the result of Setup: the running env plus typed handles to
// each service the app cares about.
type Bundle struct {
	Env    *testrig.Env
	PG     *postgres.Postgres
	WM     *wiremock.WireMock
	Seed   *seed.SchemaSeed
	Issuer *oidc.Issuer
}

// Setup brings up Postgres + SchemaSeed (ordered), plus WireMock and an
// in-process OIDC issuer (parallel), and returns the bundle plus a cleanup
// function. Cleanup is idempotent. WireMock is always started in verbose
// mode so demo and test runs alike surface per-request traffic in the
// testrig log stream.
func Setup(ctx context.Context) (*Bundle, func(), error) {
	pg := postgres.New("pg").
		WithDatabase("viper_app").
		WithDSNPropertyName("DATABASE_URL")
	wm := wiremock.New("wm").
		WithURLPropertyName("REMOTE_URL").
		WithVerboseLogging()
	issuer := oidc.New("idp").
		WithAllowedAudiences(Audience).
		WithIssuerURLPropertyName("OIDC_ISSUER_URL").
		WithJWKSURLPropertyName("OIDC_JWKS_URL").
		WithAudiencePropertyName("OIDC_AUDIENCE")
	seedSvc := seed.New(pg)

	env := testrig.New("viper-app").
		WithStages(testrig.NewStages(pg).Then(seedSvc)).
		With(wm).
		With(issuer)

	if _, err := env.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("env.Start: %w", err)
	}
	cleanup := func() { _ = env.Stop(context.Background()) }
	return &Bundle{Env: env, PG: pg, WM: wm, Seed: seedSvc, Issuer: issuer}, cleanup, nil
}
