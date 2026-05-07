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
