package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sha1n/testrig-go/internal/testutil"
	"github.com/sha1n/testrig-go/pkg/testrig/testkits/postgres"
)

func TestTestkit_Defaults(t *testing.T) {
	tk := postgres.New("test-db")

	if tk.Name() != "test-db" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}
	if !strings.HasPrefix(tk.Identifier(), "postgres:") {
		t.Errorf("Unexpected identifier prefix: %s", tk.Identifier())
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

func TestTestkit_Configured(t *testing.T) {
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

func TestTestkit_DSNProperty_URLEncodesSpecialChars(t *testing.T) {
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

func TestTestkit_Identifier_StableAndCollisionResistant(t *testing.T) {
	a := postgres.New("svc").WithPassword("foo:bar")
	b := postgres.New("svc").WithPassword("foo:bar")
	c := postgres.New("svc").WithPassword("foo:baz")

	if a.Identifier() != b.Identifier() {
		t.Error("Same config should yield same identifier")
	}
	if a.Identifier() == c.Identifier() {
		t.Error("Different password should yield different identifier")
	}
}

func TestTestkit_StartTwice_ReturnsError(t *testing.T) {
	tk := postgres.New("twice")
	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err == nil {
		t.Error("Expected error on second Start")
	}
}

func TestTestkit_Start_Error(t *testing.T) {
	tk := postgres.New("err-db").WithImage("non-existent-image-12345")
	_, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
}

func TestTestkit_Stop_NoContainer(t *testing.T) {
	tk := postgres.New("no-container")
	if err := tk.Stop(context.Background()); err != nil {
		t.Errorf("Stop without container should be no-op, got %v", err)
	}
}

func TestTestkit_DSN_MatchesProperty(t *testing.T) {
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

func TestTestkit_DB_PingsAndReturnsConnection(t *testing.T) {
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
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Errorf("Ping on returned DB failed: %v", err)
	}
}
