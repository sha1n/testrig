package main

import (
	"context"
	"testing"

	"github.com/sha1n/testrig-go/pkg/testrig"
	"github.com/sha1n/testrig-go/pkg/testrig/testkits/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViperConfigLoading(t *testing.T) {
	pg := postgres.New("pg").WithDatabase("viper_db")

	env := testrig.MustNew(testrig.With(pg))
	require.NoError(t, env.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, env.Stop(context.Background())) })

	// Parallel-safe: properties are injected via viper.Set (in-memory),
	// not via os.Setenv. Each test gets its own viper instance.
	//
	// The Postgres testkit exports its DSN under the fixed key "pg.dsn"; the
	// application's config expects "DATABASE_URL". This test bridges from the
	// testkit's vocabulary to the application's vocabulary at the consumption
	// site — typical pattern when reusing a generic testkit across apps that
	// each have their own config keys.
	props := env.Properties()
	overrides := map[string]string{
		"DATABASE_URL": props["pg.dsn"],
		"APP_PORT":     "8080",
	}

	cfg, err := LoadConfig(overrides)
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.AppPort)
	assert.Contains(t, cfg.DatabaseURL, "viper_db")
	assert.Contains(t, cfg.DatabaseURL, "postgres://")
}
