package seed_test

import (
	"context"
	"testing"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/examples/internal/seed"
	"github.com/sha1n/testrig/services/postgres"
)

// TestSchemaSeed_AppliesSchema verifies the seed service runs DDL against
// Postgres and publishes its applied marker.
func TestSchemaSeed_AppliesSchema(t *testing.T) {
	pg := postgres.New("pg").WithDatabase("seed_test")
	s := seed.New(pg)

	env := testrig.New("seed-test").
		WithStages(testrig.NewStages(pg).Then(s))

	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	props := env.Properties()
	if props["seed.applied"] != "true" {
		t.Errorf("expected seed.applied=true, got %q", props["seed.applied"])
	}

	// Verify the table exists.
	db, err := pg.DB(context.Background())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM responses`).Scan(&count); err != nil {
		t.Fatalf("query responses: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows in fresh table, got %d", count)
	}
}

// TestSchemaSeed_Name verifies the Name accessor returns the expected value.
func TestSchemaSeed_Name(t *testing.T) {
	pg := postgres.New("pg")
	s := seed.New(pg)
	if got := s.Name(); got != "seed" {
		t.Errorf("Name() = %q, want %q", got, "seed")
	}
}

// TestSchemaSeed_Stop verifies Stop is a no-op and returns nil.
func TestSchemaSeed_Stop(t *testing.T) {
	pg := postgres.New("pg")
	s := seed.New(pg)
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop() returned unexpected error: %v", err)
	}
}

// TestSchemaSeed_Idempotent verifies that starting the environment a second
// time (applying the schema again) does not fail because the DDL uses
// CREATE TABLE IF NOT EXISTS.
func TestSchemaSeed_Idempotent(t *testing.T) {
	pg := postgres.New("pg").WithDatabase("seed_idempotent_test")
	s := seed.New(pg)

	env := testrig.New("seed-idempotent").
		WithStages(testrig.NewStages(pg).Then(s))

	// First start.
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}

	// Second start against the same container — should not fail.
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	props := env.Properties()
	if props["seed.applied"] != "true" {
		t.Errorf("expected seed.applied=true after second start, got %q", props["seed.applied"])
	}
}
