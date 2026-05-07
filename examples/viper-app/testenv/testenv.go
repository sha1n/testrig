// Package testenv wires the viper-app's test environment via testrig.
// Setup is the single integration surface — both main and the server
// integration tests call it.
package testenv

import (
	"context"
	"fmt"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/examples/internal/seed"
	"github.com/sha1n/testrig/services/postgres"
	"github.com/sha1n/testrig/services/wiremock"
)

// Bundle is the result of Setup: the running env plus typed handles to
// each service the app cares about.
type Bundle struct {
	Env  *testrig.Env
	PG   *postgres.Postgres
	WM   *wiremock.WireMock
	Seed *seed.SchemaSeed
}

// Setup brings up Postgres + SchemaSeed (in a single ordered track) and
// WireMock (in a parallel track), and returns the bundle plus a cleanup
// function. Cleanup is idempotent.
func Setup(ctx context.Context) (*Bundle, func(), error) {
	pg := postgres.New("pg").
		WithDatabase("viper_app").
		WithDSNPropertyName("DATABASE_URL")
	wm := wiremock.New("wm").
		WithURLPropertyName("REMOTE_URL")
	seedSvc := seed.New(pg)

	env := testrig.New("viper-app").
		WithStages(testrig.NewStages(pg).Then(seedSvc)).
		With(wm)

	if err := env.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("env.Start: %w", err)
	}
	cleanup := func() { _ = env.Stop(context.Background()) }
	return &Bundle{Env: env, PG: pg, WM: wm, Seed: seedSvc}, cleanup, nil
}
