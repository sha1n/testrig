// Package postgres provides a Testkit backed by a Testcontainers PostgreSQL
// container. The Testkit implements testrig.Service.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sha1n/testrig-go/pkg/testrig"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultImage      = "postgres"
	defaultTag        = "16-alpine"
	defaultDBName     = "testdb"
	defaultDBUser     = "user"
	defaultDBPassword = "password"
)

// Testkit is a pre-configured Postgres test harness. It implements
// testrig.Service so it can be added to a testrig.Env, and (in a later commit)
// exposes typed-client accessors usable once the env has started.
//
// Construct with New, configure via the With* methods (chainable), then pass
// to env.With(...). Calling Start more than once returns an error.
type Testkit struct {
	name       string
	image      string
	tag        string
	dbName     string
	dbUser     string
	dbPassword string
	logger     *slog.Logger

	// runtime state, populated during Start.
	container *postgres.PostgresContainer
	host      string
	port      string
	started   bool
}

// New creates a Postgres Testkit with default configuration.
func New(name string) *Testkit {
	return &Testkit{
		name:       name,
		image:      defaultImage,
		tag:        defaultTag,
		dbName:     defaultDBName,
		dbUser:     defaultDBUser,
		dbPassword: defaultDBPassword,
		logger:     slog.Default(),
	}
}

// WithImage sets the Docker image name.
func (t *Testkit) WithImage(image string) *Testkit { t.image = image; return t }

// WithTag sets the Docker image tag.
func (t *Testkit) WithTag(tag string) *Testkit { t.tag = tag; return t }

// WithDatabase sets the database created on Start.
func (t *Testkit) WithDatabase(name string) *Testkit { t.dbName = name; return t }

// WithUsername sets the database username.
func (t *Testkit) WithUsername(user string) *Testkit { t.dbUser = user; return t }

// WithPassword sets the database password.
func (t *Testkit) WithPassword(pass string) *Testkit { t.dbPassword = pass; return t }

// Name implements testrig.Service.
func (t *Testkit) Name() string { return t.name }

// Identifier returns a content-addressed identifier over the testkit config.
// SHA-256 of a NUL-separated encoding so no character in any field can break it.
func (t *Testkit) Identifier() string {
	parts := []string{"postgres", t.image, t.tag, t.name, t.dbName, t.dbUser, t.dbPassword}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "postgres:" + hex.EncodeToString(sum[:])
}

// Dependencies implements testrig.Service. Postgres is a leaf service.
func (t *Testkit) Dependencies() []string { return nil }

// Start implements testrig.Service. Returns an error if called twice.
func (t *Testkit) Start(ctx context.Context, envCtx testrig.TestEnvContext) (testrig.Properties, error) {
	if t.started {
		return nil, fmt.Errorf("postgres testkit %q already started", t.name)
	}
	t.logger = envCtx.Logger()
	t.logger.Info("🎬 Starting Postgres testkit", "name", t.name)

	container, err := postgres.Run(ctx,
		fmt.Sprintf("%s:%s", t.image, t.tag),
		postgres.WithDatabase(t.dbName),
		postgres.WithUsername(t.dbUser),
		postgres.WithPassword(t.dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(30*time.Second),
			)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start postgres container: %w", err)
	}
	t.container = container
	t.started = true

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres host: %w", err)
	}
	t.host = host

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres port: %w", err)
	}
	t.port = port.Port()

	return testrig.Properties{
		t.name + ".host":     t.host,
		t.name + ".port":     t.port,
		t.name + ".user":     t.dbUser,
		t.name + ".password": t.dbPassword,
		t.name + ".dbname":   t.dbName,
		t.name + ".dsn":      t.dsn(),
	}, nil
}

// Stop implements testrig.Service. Safe to call before Start.
func (t *Testkit) Stop(ctx context.Context) error {
	t.logger.Info("🛑 Stopping Postgres testkit", "name", t.name)
	if t.container != nil {
		return t.container.Terminate(ctx)
	}
	return nil
}

// DSN returns the canonical PostgreSQL DSN. Only valid after Start.
func (t *Testkit) DSN() string { return t.dsn() }

// DB opens a *sql.DB connected to the Postgres container and verifies the
// connection by Pinging it. Only valid after Start.
//
// sql.Open by itself does not dial — it just parses the DSN — so this method
// returns a real connection error rather than a dial-deferred handle.
func (t *Testkit) DB(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open("pgx", t.dsn())
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}
	return db, nil
}

// dsn builds the canonical DSN using net/url so credentials and db names with
// special characters round-trip correctly. Used both internally (to populate
// the dsn property in Start) and by the public DSN() accessor.
func (t *Testkit) dsn() string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(t.dbUser, t.dbPassword),
		Host:     fmt.Sprintf("%s:%s", t.host, t.port),
		Path:     t.dbName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}
