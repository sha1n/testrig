package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sha1n/testrig/internal/testutil"
	"github.com/sha1n/testrig/services/postgres"
)

func TestPostgres_Defaults(t *testing.T) {
	tk := postgres.New("test-db")

	if tk.Name() != "test-db" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}
	if len(tk.Dependencies()) != 0 {
		t.Error("Expected no dependencies")
	}

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if props["test-db.host"] != "localhost" {
		t.Errorf("Expected host localhost, got %s", props["test-db.host"])
	}
	if props["test-db.port"] == "" {
		t.Error("Expected port to be populated")
	}
	if props["test-db.user"] != "user" {
		t.Errorf("Expected default user, got %s", props["test-db.user"])
	}
	if props["test-db.dbname"] != "testdb" {
		t.Errorf("Expected default dbname, got %s", props["test-db.dbname"])
	}
	dsn := props["test-db.dsn"]
	if !strings.HasPrefix(dsn, "postgres://user:password@localhost:") {
		t.Errorf("Expected DSN prefix, got: %s", dsn)
	}
	if !strings.HasSuffix(dsn, "/testdb?sslmode=disable") {
		t.Errorf("Expected DSN suffix, got: %s", dsn)
	}
}

func TestPostgres_Configured(t *testing.T) {
	tk := postgres.New("custom-db").
		WithImage("postgres").
		WithTag("15-alpine").
		WithDatabase("custom-dbname").
		WithUsername("custom-user").
		WithPassword("custom-password")

	if tk.Name() != "custom-db" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if props["custom-db.user"] != "custom-user" {
		t.Errorf("Expected user custom-user, got %s", props["custom-db.user"])
	}
	if props["custom-db.password"] != "custom-password" {
		t.Errorf("Expected password, got %s", props["custom-db.password"])
	}
	if props["custom-db.dbname"] != "custom-dbname" {
		t.Errorf("Expected dbname, got %s", props["custom-db.dbname"])
	}
	dsn := props["custom-db.dsn"]
	if !strings.HasPrefix(dsn, "postgres://custom-user:custom-password@localhost:") {
		t.Errorf("Expected DSN prefix, got: %s", dsn)
	}
	if !strings.HasSuffix(dsn, "/custom-dbname?sslmode=disable") {
		t.Errorf("Expected DSN suffix, got: %s", dsn)
	}
}

func TestPostgres_DSNProperty_URLEncodesSpecialChars(t *testing.T) {
	tk := postgres.New("special").WithPassword("p@ss/word:1")

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	dsn := props["special.dsn"]
	// '@' encoded as %40, '/' as %2F, ':' as %3A in the userinfo segment.
	if !strings.Contains(dsn, "p%40ss%2Fword%3A1@") {
		t.Errorf("DSN userinfo not URL-encoded; got %s", dsn)
	}
}

func TestPostgres_PropertyNameOverrides(t *testing.T) {
	tk := postgres.New("pg").
		WithDatabase("app_db").
		WithDSNPropertyName("DATABASE_URL").
		WithHostPropertyName("DB_HOST").
		WithPortPropertyName("DB_PORT").
		WithUsernamePropertyName("DB_USER").
		WithPasswordPropertyName("DB_PASSWORD").
		WithDatabasePropertyName("DB_NAME")

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if props["DATABASE_URL"] == "" {
		t.Error("DATABASE_URL property not published under custom key")
	}
	if props["DB_HOST"] == "" {
		t.Error("DB_HOST property not published under custom key")
	}
	if props["DB_PORT"] == "" {
		t.Error("DB_PORT property not published under custom key")
	}
	if props["DB_USER"] != "user" {
		t.Errorf("DB_USER = %q, want \"user\"", props["DB_USER"])
	}
	if props["DB_PASSWORD"] != "password" {
		t.Errorf("DB_PASSWORD = %q, want \"password\"", props["DB_PASSWORD"])
	}
	if props["DB_NAME"] != "app_db" {
		t.Errorf("DB_NAME = %q, want \"app_db\"", props["DB_NAME"])
	}

	// Default keys should NOT be present when overridden.
	for _, k := range []string{"pg.host", "pg.port", "pg.user", "pg.password", "pg.dbname", "pg.dsn"} {
		if _, ok := props[k]; ok {
			t.Errorf("default key %q should not be published when overridden", k)
		}
	}
}

func TestPostgres_StartTwice_ReturnsError(t *testing.T) {
	tk := postgres.New("twice")
	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err == nil {
		t.Error("Expected error on second Start")
	}
}

func TestPostgres_StopThenStart_Succeeds(t *testing.T) {
	// A service instance must be reusable across env restart cycles. Stop
	// releases the container and clears service state so a subsequent Start
	// builds a fresh one.
	tk := postgres.New("restart-test")
	ctx := context.Background()

	if _, err := tk.Start(ctx, &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if _, err := tk.Start(ctx, &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("second Start after Stop must succeed; got: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestPostgres_Start_Error(t *testing.T) {
	tk := postgres.New("err-db").WithImage("non-existent-image-12345")
	_, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
}

func TestPostgres_Stop_NoContainer(t *testing.T) {
	tk := postgres.New("no-container")
	if err := tk.Stop(context.Background()); err != nil {
		t.Errorf("Stop without container should be no-op, got %v", err)
	}
}

func TestPostgres_DSN_MatchesProperty(t *testing.T) {
	tk := postgres.New("dsn-match").WithPassword("p@ss/word:1")

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.DSN() != props["dsn-match.dsn"] {
		t.Errorf("DSN() and dsn-match.dsn property should match.\nDSN(): %s\nprop:  %s", tk.DSN(), props["dsn-match.dsn"])
	}
}

func TestPostgres_DB_PingsAndReturnsConnection(t *testing.T) {
	tk := postgres.New("db-test")

	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	db, err := tk.DB(context.Background())
	if err != nil {
		t.Fatalf("DB failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil *sql.DB")
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		t.Errorf("Ping on returned DB failed: %v", err)
	}
}

func TestPostgres_DB_PingError_PropagatesContextCancel(t *testing.T) {
	// DB() Pings before returning — verify a cancelled context surfaces as
	// an error from DB() rather than a deferred dial failure on first query.
	tk := postgres.New("db-ping-fail")
	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before DB() runs Ping

	if _, err := tk.DB(ctx); err == nil {
		t.Fatal("expected error from DB() with cancelled context")
	}
}
