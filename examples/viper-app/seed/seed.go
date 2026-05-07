// Package seed provides a custom testrig.Service that applies schema DDL
// to a Postgres service. It is non-dockerized — it runs in the test
// process and depends on a *postgres.Postgres being already started.
//
// Wire with WithStages so the seed runs only after Postgres is ready:
//
//	env := testrig.New("app").
//	    WithStages(testrig.NewStages(pg).Then(seed.New(pg)))
package seed

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/postgres"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS responses (
    key        TEXT PRIMARY KEY,
    response   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// SchemaSeed implements testrig.Service. Construct with New, register
// with Env.WithStages after the Postgres service it depends on.
type SchemaSeed struct {
	name string
	pg   *postgres.Postgres
}

// New creates a SchemaSeed that will seed the given Postgres service on Start.
func New(pg *postgres.Postgres) *SchemaSeed {
	return &SchemaSeed{name: "seed", pg: pg}
}

// Name implements testrig.Service.
func (s *SchemaSeed) Name() string { return s.name }

// Start opens a connection to the Postgres service and applies the schema.
// Publishes the property "seed.applied" = "true" so tests can assert that
// the service ran.
func (s *SchemaSeed) Start(ctx context.Context, logger *slog.Logger) (testrig.Properties, error) {
	logger.Info("applying schema")
	db, err := s.pg.DB(ctx)
	if err != nil {
		return nil, fmt.Errorf("seed: open postgres: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := apply(ctx, db, schemaDDL); err != nil {
		return nil, fmt.Errorf("seed: apply schema: %w", err)
	}
	return testrig.Properties{"seed.applied": "true"}, nil
}

// Stop is a no-op: the schema dies with its container.
func (s *SchemaSeed) Stop(ctx context.Context) error { return nil }

// apply executes a single SQL statement.
func apply(ctx context.Context, db *sql.DB, stmt string) error {
	_, err := db.ExecContext(ctx, stmt)
	return err
}
