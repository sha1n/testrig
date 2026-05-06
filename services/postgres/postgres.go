// Package postgres provides a PostgreSQL service backed by Testcontainers.
// The exported Postgres type implements testrig.Service.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sha1n/testrig"
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

// Postgres is a pre-configured PostgreSQL test harness. It implements
// testrig.Service so it can be added to a testrig.Env, and exposes typed-client
// accessors (DSN, DB) usable once the env has started.
//
// Construct with New, configure via the With* methods (chainable), then pass
// to env.With(...). A Postgres instance is reusable across Start/Stop cycles:
// Stop releases the container so a subsequent Start builds a fresh one.
// Calling Start without Stop in between returns an error.
//
// Property keys default to "<name>.host", "<name>.port", "<name>.user",
// "<name>.password", "<name>.dbname", "<name>.dsn". Each is independently
// overridable via WithXxxPropertyName so tests can wire the service's outputs
// into the application's own config keys (e.g. "DATABASE_URL") with no
// bridging step.
type Postgres struct {
	name       string
	image      string
	tag        string
	dbName     string
	dbUser     string
	dbPassword string
	logger     *slog.Logger

	hostPropName     string
	portPropName     string
	userPropName     string
	passwordPropName string
	dbNamePropName   string
	dsnPropName      string

	// Runtime state, populated during Start and cleared by Stop.
	// container != nil is the canonical "currently running" check.
	container *postgres.PostgresContainer
	host      string
	port      string
}

// New creates a Postgres service with default configuration. Property keys
// default to "<name>.<field>"; override individual keys via the
// WithXxxPropertyName setters.
func New(name string) *Postgres {
	return &Postgres{
		name:             name,
		image:            defaultImage,
		tag:              defaultTag,
		dbName:           defaultDBName,
		dbUser:           defaultDBUser,
		dbPassword:       defaultDBPassword,
		logger:           slog.Default(),
		hostPropName:     name + ".host",
		portPropName:     name + ".port",
		userPropName:     name + ".user",
		passwordPropName: name + ".password",
		dbNamePropName:   name + ".dbname",
		dsnPropName:      name + ".dsn",
	}
}

// WithImage sets the Docker image name.
func (t *Postgres) WithImage(image string) *Postgres { t.image = image; return t }

// WithTag sets the Docker image tag.
func (t *Postgres) WithTag(tag string) *Postgres { t.tag = tag; return t }

// WithDatabase sets the database created on Start.
func (t *Postgres) WithDatabase(name string) *Postgres { t.dbName = name; return t }

// WithUsername sets the database username.
func (t *Postgres) WithUsername(user string) *Postgres { t.dbUser = user; return t }

// WithPassword sets the database password.
func (t *Postgres) WithPassword(pass string) *Postgres { t.dbPassword = pass; return t }

// WithHostPropertyName sets the property key under which the container host
// is published. Default: "<name>.host".
func (t *Postgres) WithHostPropertyName(name string) *Postgres {
	t.hostPropName = name
	return t
}

// WithPortPropertyName sets the property key under which the container port
// is published. Default: "<name>.port".
func (t *Postgres) WithPortPropertyName(name string) *Postgres {
	t.portPropName = name
	return t
}

// WithUsernamePropertyName sets the property key under which the database
// username is published. Default: "<name>.user".
func (t *Postgres) WithUsernamePropertyName(name string) *Postgres {
	t.userPropName = name
	return t
}

// WithPasswordPropertyName sets the property key under which the database
// password is published. Default: "<name>.password".
func (t *Postgres) WithPasswordPropertyName(name string) *Postgres {
	t.passwordPropName = name
	return t
}

// WithDatabasePropertyName sets the property key under which the database
// name is published. Default: "<name>.dbname".
func (t *Postgres) WithDatabasePropertyName(name string) *Postgres {
	t.dbNamePropName = name
	return t
}

// WithDSNPropertyName sets the property key under which the fully-constructed
// DSN string is published. Default: "<name>.dsn".
//
// Use this to publish the DSN directly under the application's expected
// config key (e.g. "DATABASE_URL") so the test does not need to bridge
// between service-published and application-expected vocabularies.
func (t *Postgres) WithDSNPropertyName(name string) *Postgres {
	t.dsnPropName = name
	return t
}

// Name implements testrig.Service.
func (t *Postgres) Name() string { return t.name }

// Dependencies implements testrig.Service. Postgres is a leaf service.
func (t *Postgres) Dependencies() []string { return nil }

// Start implements testrig.Service. Returns an error if called while a
// previous Start is still active (i.e. Stop has not been called).
func (t *Postgres) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	if t.container != nil {
		return nil, fmt.Errorf("postgres service %q already started", t.name)
	}
	t.logger = envCtx.Logger()
	t.logger.Info("🎬 Starting Postgres service", "name", t.name)

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
		t.hostPropName:     t.host,
		t.portPropName:     t.port,
		t.userPropName:     t.dbUser,
		t.passwordPropName: t.dbPassword,
		t.dbNamePropName:   t.dbName,
		t.dsnPropName:      t.dsn(),
	}, nil
}

// Stop implements testrig.Service. Safe to call before Start or twice in
// a row. Releases the container and clears runtime state so the service can
// be Started again.
func (t *Postgres) Stop(ctx context.Context) error {
	if t.container == nil {
		return nil
	}
	t.logger.Info("🛑 Stopping Postgres service", "name", t.name)
	err := t.container.Terminate(ctx)
	t.container = nil
	t.host = ""
	t.port = ""
	return err
}

// DSN returns the canonical PostgreSQL DSN. Only valid after Start.
func (t *Postgres) DSN() string { return t.dsn() }

// DB opens a *sql.DB connected to the Postgres container and verifies the
// connection by Pinging it. Only valid after Start.
//
// sql.Open by itself does not dial — it just parses the DSN — so this method
// returns a real connection error rather than a dial-deferred handle.
func (t *Postgres) DB(ctx context.Context) (*sql.DB, error) {
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
func (t *Postgres) dsn() string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(t.dbUser, t.dbPassword),
		Host:     fmt.Sprintf("%s:%s", t.host, t.port),
		Path:     t.dbName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}
