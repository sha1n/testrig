package postgres

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/sha1n/testrig/api"
	"github.com/sha1n/testrig/dockerlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

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

func newCapturingLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
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

type fakeContainer struct {
	testcontainers.Container
	t                *testing.T
	getContainerIDFn func() string
	hostFn           func(context.Context) (string, error)
	mappedPortFn     func(context.Context, string) (network.Port, error)
	terminateFn      func(context.Context) error
}

func (f *fakeContainer) GetContainerID() string {
	if f.getContainerIDFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.GetContainerID")
		return ""
	}
	return f.getContainerIDFn()
}

func (f *fakeContainer) Host(ctx context.Context) (string, error) {
	if f.hostFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.Host")
		return "", nil
	}
	return f.hostFn(ctx)
}

func (f *fakeContainer) MappedPort(ctx context.Context, port string) (network.Port, error) {
	if f.mappedPortFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.MappedPort")
		return network.Port{}, nil
	}
	return f.mappedPortFn(ctx, port)
}

func (f *fakeContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	if f.terminateFn == nil {
		f.t.Fatal("unexpected call to fakeContainer.Terminate")
		return nil
	}
	return f.terminateFn(ctx)
}

func TestPostgres_Unit_Defaults(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
		t:      t,
		hostFn: func(_ context.Context) (string, error) { return "localhost", nil },
		mappedPortFn: func(_ context.Context, _ string) (network.Port, error) {
			return network.MustParsePort("5432/tcp"), nil
		},
		terminateFn: func(_ context.Context) error { terminated = true; return nil },
	}

	pg := New("unit-db")
	pg.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (*postgres.PostgresContainer, error) {
		return &postgres.PostgresContainer{Container: fake}, nil
	}

	props, err := pg.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)

	assert.Equal(t, "localhost", props["unit-db.host"])
	assert.Equal(t, "5432", props["unit-db.port"])
	assert.Equal(t, "user", props["unit-db.user"])
	assert.Equal(t, "testdb", props["unit-db.dbname"])

	dsn := props["unit-db.dsn"]
	assert.True(t, strings.HasPrefix(dsn, "postgres://user:password@localhost:5432"))
	assert.Equal(t, dsn, pg.DSN())

	err = pg.Stop(context.Background())
	require.NoError(t, err)
	assert.True(t, terminated, "expected container to be terminated on Stop")
}

func TestPostgres_Unit_HostFailure_CleansUp(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
		t:           t,
		hostFn:      func(_ context.Context) (string, error) { return "", errors.New("host error") },
		terminateFn: func(_ context.Context) error { terminated = true; return nil },
	}

	pg := New("unit-db")
	pg.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (*postgres.PostgresContainer, error) {
		return &postgres.PostgresContainer{Container: fake}, nil
	}

	_, err := pg.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to get postgres host")
	assert.True(t, terminated, "expected container to be terminated on Start failure")
	assert.Nil(t, pg.container, "expected pg.container to be nil after cleanup")
}

func TestPostgres_Unit_PortFailure_CleansUp(t *testing.T) {
	terminated := false
	fake := &fakeContainer{
		t:      t,
		hostFn: func(_ context.Context) (string, error) { return "localhost", nil },
		mappedPortFn: func(_ context.Context, _ string) (network.Port, error) {
			return network.Port{}, errors.New("port error")
		},
		terminateFn: func(_ context.Context) error { terminated = true; return nil },
	}

	pg := New("unit-db")
	pg.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (*postgres.PostgresContainer, error) {
		return &postgres.PostgresContainer{Container: fake}, nil
	}

	_, err := pg.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to get postgres port")
	assert.True(t, terminated, "expected container to be terminated on Start failure")
	assert.Nil(t, pg.container, "expected pg.container to be nil after cleanup")
}

func TestPostgres_Unit_LogStreaming_ForwardsLogs(t *testing.T) {
	fake := &fakeContainer{
		t:                t,
		getContainerIDFn: func() string { return "fake-cid" },
		hostFn:           func(_ context.Context) (string, error) { return "localhost", nil },
		mappedPortFn:     func(_ context.Context, _ string) (network.Port, error) { return network.MustParsePort("5432/tcp"), nil },
		terminateFn:      func(_ context.Context) error { return nil },
	}

	pg := New("unit-db").WithLogStreaming()
	pg.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (*postgres.PostgresContainer, error) {
		return &postgres.PostgresContainer{Container: fake}, nil
	}

	spy := dockerlog.SetupTestSpy(&pg.logSupervisor)

	logger, buf := newCapturingLogger()
	_, err := pg.Start(context.Background(), api.StubEnvHandle("test", logger, nil))
	require.NoError(t, err)

	// Wait for supervisor to start asynchronously
	require.True(t, eventually(5*time.Second, func() bool { return spy.Started() }), "expected supervisor to be started")
	assert.Equal(t, "fake-cid", spy.ContainerID(), "expected container ID 'fake-cid'")

	sentinel := "database system is ready to accept connections"
	err = spy.WriteStdout(sentinel + "\n")
	require.NoError(t, err)

	// Wait for the log to show up in the logger's buffer
	assert.True(t, eventually(5*time.Second, func() bool { return strings.Contains(buf.String(), sentinel) }), "expected log buffer to contain sentinel")

	err = pg.Stop(context.Background())
	require.NoError(t, err)

	assert.True(t, spy.Cancelled(), "expected supervisor to be cancelled on Stop")
}

func TestPostgres_Unit_DB_PingError_PropagatesError(t *testing.T) {
	fake := &fakeContainer{
		t:      t,
		hostFn: func(_ context.Context) (string, error) { return "localhost", nil },
		mappedPortFn: func(_ context.Context, _ string) (network.Port, error) {
			// use a closed or non-listening port to force ping error
			return network.MustParsePort("9999/tcp"), nil
		},
		terminateFn: func(_ context.Context) error { return nil },
	}

	pg := New("unit-db")
	pg.containerRunFn = func(_ context.Context, _ string, _ ...testcontainers.ContainerCustomizer) (*postgres.PostgresContainer, error) {
		return &postgres.PostgresContainer{Container: fake}, nil
	}

	_, err := pg.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil))
	require.NoError(t, err)
	defer func() { _ = pg.Stop(context.Background()) }()

	// Force an immediate context cancel to ensure DB fails fast
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = pg.DB(ctx)
	assert.Error(t, err, "expected error from DB() with cancelled context")
}
