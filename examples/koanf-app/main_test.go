package main

import (
	"context"
	"testing"

	"github.com/sha1n/testrig-go/pkg/testrig"
	"github.com/sha1n/testrig-go/pkg/testrig/testkits/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKoanfConfigLoading(t *testing.T) {
	pg := postgres.New("pg").WithDatabase("koanf_db")

	env := testrig.NewEnv().With(pg)
	require.NoError(t, env.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, env.Stop(context.Background())) })

	// Parallel-safe: properties are injected via koanf's confmap.Provider
	// (in-memory), not via os.Setenv. Each test gets its own koanf instance.
	//
	// The Postgres testkit exports its DSN under the fixed key "pg.dsn"; the
	// application's config expects "DATABASE_URL". Bridge from the testkit's
	// vocabulary to the application's vocabulary at the consumption site.
	// Note that koanf treats "." as a delimiter — passing "pg.dsn" verbatim
	// would create a nested map, not a flat key.
	props := env.Properties()
	overrides := map[string]string{
		"DATABASE_URL": props["pg.dsn"],
		"APP_PORT":     "9090",
	}

	cfg, err := LoadConfig(overrides)
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.AppPort)
	assert.Contains(t, cfg.DatabaseURL, "koanf_db")
	assert.Contains(t, cfg.DatabaseURL, "postgres://")
}
