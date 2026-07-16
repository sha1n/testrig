package postgres_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/testrig/api"
	"github.com/sha1n/testrig/services/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestPostgres_Defaults(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("test-db")

	assert.Equal(t, "test-db", tk.Name())

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	assert.Equal(t, "localhost", props["test-db.host"])
	assert.NotEmpty(t, props["test-db.port"])
	assert.Equal(t, "user", props["test-db.user"])
	assert.Equal(t, "testdb", props["test-db.dbname"])

	dsn := props["test-db.dsn"]
	assert.True(t, strings.HasPrefix(dsn, "postgres://user:password@localhost:"))
	assert.True(t, strings.HasSuffix(dsn, "/testdb?sslmode=disable"))
}

func TestPostgres_Configured(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("custom-db").
		WithImage("postgres").
		WithTag("15-alpine").
		WithDatabase("custom-dbname").
		WithUsername("custom-user").
		WithPassword("custom-password")

	assert.Equal(t, "custom-db", tk.Name())

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	assert.Equal(t, "custom-user", props["custom-db.user"])
	assert.Equal(t, "custom-password", props["custom-db.password"])
	assert.Equal(t, "custom-dbname", props["custom-db.dbname"])

	dsn := props["custom-db.dsn"]
	assert.True(t, strings.HasPrefix(dsn, "postgres://custom-user:custom-password@localhost:"))
	assert.True(t, strings.HasSuffix(dsn, "/custom-dbname?sslmode=disable"))
}

func TestPostgres_DSNProperty_URLEncodesSpecialChars(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("special").WithPassword("p@ss/word:1")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	dsn := props["special.dsn"]
	assert.Contains(t, dsn, "p%40ss%2Fword%3A1@")
}

func TestPostgres_PropertyNameOverrides(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("pg").
		WithDatabase("app_db").
		WithDSNPropertyName("DATABASE_URL").
		WithHostPropertyName("DB_HOST").
		WithPortPropertyName("DB_PORT").
		WithUsernamePropertyName("DB_USER").
		WithPasswordPropertyName("DB_PASSWORD").
		WithDatabasePropertyName("DB_NAME")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	assert.NotEmpty(t, props["DATABASE_URL"])
	assert.NotEmpty(t, props["DB_HOST"])
	assert.NotEmpty(t, props["DB_PORT"])
	assert.Equal(t, "user", props["DB_USER"])
	assert.Equal(t, "password", props["DB_PASSWORD"])
	assert.Equal(t, "app_db", props["DB_NAME"])

	// Default keys should NOT be present when overridden.
	for _, k := range []string{"pg.host", "pg.port", "pg.user", "pg.password", "pg.dbname", "pg.dsn"} {
		assert.NotContains(t, props, k)
	}
}

func TestPostgres_StartTwice_ReturnsError(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("twice")
	_, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	_, err = tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	assert.Error(t, err, "Expected error on second Start")
}

func TestPostgres_StopThenStart_Succeeds(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("restart-test")
	ctx := context.Background()

	_, err := tk.Start(ctx, api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)

	err = tk.Stop(ctx)
	require.NoError(t, err)

	_, err = tk.Start(ctx, api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)

	err = tk.Stop(ctx)
	require.NoError(t, err)
}

func TestPostgres_Start_Error(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("err-db").WithImage("non-existent-image-12345")
	_, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	assert.Error(t, err, "Expected error for non-existent image")
}

func TestPostgres_Stop_NoContainer(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("no-container")
	err := tk.Stop(context.Background())
	assert.NoError(t, err, "Stop without container should be no-op")
}

func TestPostgres_DSN_MatchesProperty(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("dsn-match").WithPassword("p@ss/word:1")

	props, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	assert.Equal(t, props["dsn-match.dsn"], tk.DSN())
}

func TestPostgres_DB_PingsAndReturnsConnection(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("db-test")

	_, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	db, err := tk.DB(context.Background())
	require.NoError(t, err)
	require.NotNil(t, db)
	defer func() { _ = db.Close() }()

	err = db.Ping()
	assert.NoError(t, err)
}

func TestPostgres_DB_PingError_PropagatesContextCancel(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("db-ping-fail")
	_, err := tk.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = tk.Stop(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before DB() runs Ping

	_, err = tk.DB(ctx)
	assert.Error(t, err, "expected error from DB() with cancelled context")
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestPostgres_LogStreaming_ForwardsContainerOutput(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	tk := postgres.New("log-streaming-test").WithLogStreaming()

	buf := &syncBuffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(h)

	_, err := tk.Start(context.Background(), api.StubEnvHandle("test", logger, nil))
	if err != nil {
		if strings.Contains(err.Error(), "failed to start postgres container") || strings.Contains(err.Error(), "Docker") || strings.Contains(err.Error(), "provider") {
			t.Skip("Docker is not available; skipping integration test")
		}
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	sentinel := "database system is ready to accept connections"
	assert.True(t, eventually(15*time.Second, func() bool { return strings.Contains(buf.String(), sentinel) }), "expected logs to contain sentinel")
}
