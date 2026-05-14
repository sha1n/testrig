package sampleapp_test

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/examples/internal/sampleapp"
	"github.com/sha1n/testrig/examples/internal/seed"
	"github.com/sha1n/testrig/services/postgres"
)

// Shared across DB-dependent tests — started once in TestMain.
var (
	sharedDB  *sql.DB
	sharedEnv *testrig.Env
	pgSvc     *postgres.Postgres
)

// Tests that need specific remote behaviour create their own httptest.Server
// via makeRemote rather than sharing one.

// TestMain spins up a real Postgres container via testrig, seeds the schema,
// and runs all tests against the shared DB. The handler under test is wired
// to a per-test fake remote, not a shared one, so each test controls the
// remote behaviour independently.
func TestMain(m *testing.M) {
	ctx := context.Background()

	pgSvc = postgres.New("pg").
		WithDatabase("sampleapp_test").
		WithDSNPropertyName("DATABASE_URL")
	seedSvc := seed.New(pgSvc)

	sharedEnv = testrig.New("sampleapp-test").
		WithStages(testrig.NewStages(pgSvc).Then(seedSvc))

	if _, err := sharedEnv.Start(ctx); err != nil {
		log.Fatalf("env.Start: %v", err)
	}

	var err error
	sharedDB, err = pgSvc.DB(ctx)
	if err != nil {
		_ = sharedEnv.Stop(context.Background())
		log.Fatalf("pgSvc.DB: %v", err)
	}

	code := m.Run()

	_ = sharedDB.Close()
	_ = sharedEnv.Stop(context.Background())
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Tests that do NOT require a running database
// ---------------------------------------------------------------------------

// TestHandleSave_MissingKey_Returns400 exercises the early validation branch
// in handleSave — no DB or remote is touched.
func TestHandleSave_MissingKey_Returns400(t *testing.T) {
	srv := sampleapp.New(nil, "http://irrelevant")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/save", nil) // no ?key=
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleLookup_MissingKey_Returns400 exercises the early validation branch
// in handleLookup — no DB or remote is touched.
func TestHandleLookup_MissingKey_Returns400(t *testing.T) {
	srv := sampleapp.New(nil, "http://irrelevant")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/lookup", nil) // no ?key=
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleLookup_DBError_Returns500 verifies that a query failure (here
// caused by a closed DB connection) is surfaced as a 500, not a panic.
func TestHandleLookup_DBError_Returns500(t *testing.T) {
	// Open with the pgx driver (registered as a side-effect of importing the
	// postgres service) using an intentionally bad DSN, then immediately close
	// so any subsequent operation returns sql.ErrConnDone.
	badDB, err := sql.Open("pgx", "postgres://localhost:1/doesnotexist?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	require.NoError(t, badDB.Close())

	srv := sampleapp.New(badDB, "http://irrelevant")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/lookup?key=anything", nil)
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ---------------------------------------------------------------------------
// Tests that use the shared Postgres DB
// ---------------------------------------------------------------------------

// makeRemote returns a started httptest.Server that responds with body and
// registers t.Cleanup to close it.
func makeRemote(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(remote.Close)
	return remote
}

// uniqueKey returns a key name unique to the calling test, preventing row
// collisions between tests sharing the same DB.
func uniqueKey(t *testing.T, suffix string) string {
	t.Helper()
	return t.Name() + "/" + suffix
}

// TestHandleLookup_UnknownKey_Returns404 verifies the not-found path in
// handleLookup when no row exists for the key.
func TestHandleLookup_UnknownKey_Returns404(t *testing.T) {
	srv := sampleapp.New(sharedDB, "http://irrelevant")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/lookup?key="+uniqueKey(t, "x"), nil)
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleLookup_Found stores a row directly then verifies handleLookup
// returns it with 200. Exercises store + lookup + the 200 branch.
func TestHandleLookup_Found(t *testing.T) {
	key := uniqueKey(t, "k")
	const body = `{"msg":"hello"}`

	remote := makeRemote(t, http.StatusOK, body)
	srv := sampleapp.New(sharedDB, remote.URL)

	// Persist via the handler first.
	saveReq := httptest.NewRequest(http.MethodPost, "/save?key="+key, nil)
	saveRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(saveRec, saveReq)
	require.Equal(t, http.StatusAccepted, saveRec.Code)

	// Poll until the async goroutine has written the row.
	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
		return err == nil && got == body
	}, 10*time.Second, 10*time.Millisecond)

	// Now look it up via the handler.
	lookReq := httptest.NewRequest(http.MethodGet, "/lookup?key="+key, nil)
	lookRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lookRec, lookReq)
	assert.Equal(t, http.StatusOK, lookRec.Code)
	assert.Equal(t, body, lookRec.Body.String())
}

// TestHandleSave_PersistsRemoteResponse is the canonical end-to-end happy-path
// test for handleSave → fetchAndStore → store.
func TestHandleSave_PersistsRemoteResponse(t *testing.T) {
	key := uniqueKey(t, "save")
	const body = `{"data":"value"}`

	remote := makeRemote(t, http.StatusOK, body)
	srv := sampleapp.New(sharedDB, remote.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/save?key="+key, nil)
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
		return err == nil && got == body
	}, 10*time.Second, 10*time.Millisecond, "row for key %q did not appear", key)
}

// TestHandleSave_UpsertExistingKey saves the same key twice with different
// remote bodies and verifies the second body overwrites the first. Exercises
// the ON CONFLICT … DO UPDATE branch in store.
func TestHandleSave_UpsertExistingKey(t *testing.T) {
	key := uniqueKey(t, "upsert")
	first := `{"v":1}`
	second := `{"v":2}`

	// First save.
	remote1 := makeRemote(t, http.StatusOK, first)
	srv1 := sampleapp.New(sharedDB, remote1.URL)
	rec := httptest.NewRecorder()
	srv1.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/save?key="+key, nil))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
		return err == nil && got == first
	}, 10*time.Second, 10*time.Millisecond, "first row for key %q did not appear", key)

	// Second save — different body.
	remote2 := makeRemote(t, http.StatusOK, second)
	srv2 := sampleapp.New(sharedDB, remote2.URL)
	rec2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/save?key="+key, nil))
	require.Equal(t, http.StatusAccepted, rec2.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
		return err == nil && got == second
	}, 10*time.Second, 10*time.Millisecond, "row for key %q was not updated to second value", key)
}

// TestHandleSave_RemoteError_NoRowStored verifies that when the remote is
// completely unreachable, fetchAndStore fails gracefully (logs the error,
// returns) and no row is persisted. Exercises the HTTP-error branch of
// fetchAndStore.
func TestHandleSave_RemoteError_NoRowStored(t *testing.T) {
	key := uniqueKey(t, "remote-err")

	// Start and immediately stop a server so its port is definitely closed.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	srv := sampleapp.New(sharedDB, dead.URL)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/save?key="+key, nil))
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Give the async goroutine time to attempt (and fail) the fetch.
	time.Sleep(200 * time.Millisecond)

	var got string
	err := sharedDB.QueryRowContext(context.Background(),
		`SELECT response FROM responses WHERE key = $1`, key).Scan(&got)
	assert.ErrorIs(t, err, sql.ErrNoRows, "expected no row for key %q after remote error", key)
}
