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
	"github.com/sha1n/testrig/postgres"
)

// Shared across DB-dependent tests — started once in TestMain.
var (
	sharedDB  *sql.DB
	sharedEnv *testrig.Env
	pgSvc     *postgres.Postgres
)

// TestMain spins up a real Postgres container via testrig, seeds the schema,
// and runs all tests against the shared DB. The handler under test is wired
// to a per-test fake remote and a per-test auth fixture so each test
// controls the remote behaviour independently.
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
// Helpers shared with auth_test.go (both files are package sampleapp_test).
// authFixture and its methods live in auth_test.go.
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

// newServer builds a sampleapp.Server with the given DB, remote URL, and the
// fixture's auth config.
func newServer(db *sql.DB, remoteURL string, fix *authFixture) *sampleapp.Server {
	return sampleapp.New(db, sampleapp.Config{
		RemoteURL: remoteURL,
		Auth:      fix.cfg,
	})
}

// authedRequest builds an HTTP request and attaches a Bearer token signed by
// the fixture for the given subject. Subject defaults to fix.subject when empty.
func authedRequest(t *testing.T, fix *authFixture, method, target, subject string) *http.Request {
	t.Helper()
	if subject == "" {
		subject = fix.subject
	}
	claims := fix.validClaims()
	claims["sub"] = subject
	tok := fix.sign(t, claims)
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

// ---------------------------------------------------------------------------
// Tests that do NOT require a running database
// ---------------------------------------------------------------------------

// TestHandleSave_MissingKey_Returns400 exercises the early validation branch
// in handleSave (after the middleware passes). No DB is touched.
func TestHandleSave_MissingKey_Returns400(t *testing.T) {
	fix := newAuthFixture(t)
	srv := newServer(nil, "http://irrelevant", fix)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodPost, "/save", ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleLookup_MissingKey_Returns400 exercises the early validation branch
// in handleLookup.
func TestHandleLookup_MissingKey_Returns400(t *testing.T) {
	fix := newAuthFixture(t)
	srv := newServer(nil, "http://irrelevant", fix)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodGet, "/lookup", ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleLookup_DBError_Returns500 verifies that a query failure is surfaced
// as a 500, not a panic.
func TestHandleLookup_DBError_Returns500(t *testing.T) {
	badDB, err := sql.Open("pgx", "postgres://localhost:1/doesnotexist?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	require.NoError(t, badDB.Close())

	fix := newAuthFixture(t)
	srv := newServer(badDB, "http://irrelevant", fix)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodGet, "/lookup?key=anything", ""))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ---------------------------------------------------------------------------
// Tests that use the shared Postgres DB
// ---------------------------------------------------------------------------

// TestHandleLookup_UnknownKey_Returns404 verifies the not-found path in
// handleLookup when no row exists for the (sub, key) pair.
func TestHandleLookup_UnknownKey_Returns404(t *testing.T) {
	fix := newAuthFixture(t)
	srv := newServer(sharedDB, "http://irrelevant", fix)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodGet, "/lookup?key="+uniqueKey(t, "x"), ""))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleLookup_Found stores a row via the save handler and verifies
// handleLookup returns it with 200. Exercises store + lookup + the 200 branch.
func TestHandleLookup_Found(t *testing.T) {
	fix := newAuthFixture(t)
	key := uniqueKey(t, "k")
	const body = `{"msg":"hello"}`

	remote := makeRemote(t, http.StatusOK, body)
	srv := newServer(sharedDB, remote.URL, fix)

	saveRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(saveRec, authedRequest(t, fix, http.MethodPost, "/save?key="+key, ""))
	require.Equal(t, http.StatusAccepted, saveRec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, fix.subject, key).Scan(&got)
		return err == nil && got == body
	}, 10*time.Second, 10*time.Millisecond)

	lookRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lookRec, authedRequest(t, fix, http.MethodGet, "/lookup?key="+key, ""))
	assert.Equal(t, http.StatusOK, lookRec.Code)
	assert.Equal(t, body, lookRec.Body.String())
}

// TestHandleSave_PersistsRemoteResponse is the canonical end-to-end happy-path
// test for handleSave → fetchAndStore → store.
func TestHandleSave_PersistsRemoteResponse(t *testing.T) {
	fix := newAuthFixture(t)
	key := uniqueKey(t, "save")
	const body = `{"data":"value"}`

	remote := makeRemote(t, http.StatusOK, body)
	srv := newServer(sharedDB, remote.URL, fix)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodPost, "/save?key="+key, ""))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, fix.subject, key).Scan(&got)
		return err == nil && got == body
	}, 10*time.Second, 10*time.Millisecond, "row for user=%s key=%s did not appear", fix.subject, key)
}

// TestHandleSave_UpsertExistingKey saves the same (user, key) pair twice with
// different remote bodies and verifies the second body overwrites the first.
// Exercises the ON CONFLICT … DO UPDATE branch in store.
func TestHandleSave_UpsertExistingKey(t *testing.T) {
	fix := newAuthFixture(t)
	key := uniqueKey(t, "upsert")
	first := `{"v":1}`
	second := `{"v":2}`

	remote1 := makeRemote(t, http.StatusOK, first)
	srv1 := newServer(sharedDB, remote1.URL, fix)
	rec := httptest.NewRecorder()
	srv1.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodPost, "/save?key="+key, ""))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, fix.subject, key).Scan(&got)
		return err == nil && got == first
	}, 10*time.Second, 10*time.Millisecond)

	remote2 := makeRemote(t, http.StatusOK, second)
	srv2 := newServer(sharedDB, remote2.URL, fix)
	rec2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec2, authedRequest(t, fix, http.MethodPost, "/save?key="+key, ""))
	require.Equal(t, http.StatusAccepted, rec2.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, fix.subject, key).Scan(&got)
		return err == nil && got == second
	}, 10*time.Second, 10*time.Millisecond, "row for user=%s key=%s was not updated", fix.subject, key)
}

// TestHandleSave_RemoteError_NoRowStored verifies that when the remote is
// completely unreachable, fetchAndStore fails gracefully and no row is persisted.
func TestHandleSave_RemoteError_NoRowStored(t *testing.T) {
	fix := newAuthFixture(t)
	key := uniqueKey(t, "remote-err")

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	srv := newServer(sharedDB, dead.URL, fix)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodPost, "/save?key="+key, ""))
	require.Equal(t, http.StatusAccepted, rec.Code)

	time.Sleep(200 * time.Millisecond)

	var got string
	err := sharedDB.QueryRowContext(context.Background(),
		`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, fix.subject, key).Scan(&got)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// TestPerUserIsolation_LookupReturns404 verifies that one user cannot read
// another user's row via /lookup — the row exists, but not in this user's
// namespace, so /lookup returns 404.
func TestPerUserIsolation_LookupReturns404(t *testing.T) {
	fix := newAuthFixture(t)
	key := uniqueKey(t, "shared-key")
	const body = `{"alice":"data"}`

	remote := makeRemote(t, http.StatusOK, body)
	srv := newServer(sharedDB, remote.URL, fix)

	// alice saves under key.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, authedRequest(t, fix, http.MethodPost, "/save?key="+key, "alice"))
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Eventually(t, func() bool {
		var got string
		err := sharedDB.QueryRowContext(context.Background(),
			`SELECT response FROM responses WHERE user_id = $1 AND key = $2`, "alice", key).Scan(&got)
		return err == nil && got == body
	}, 10*time.Second, 10*time.Millisecond)

	// bob looks up the same key — should get 404, not alice's row.
	lookRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lookRec, authedRequest(t, fix, http.MethodGet, "/lookup?key="+key, "bob"))
	assert.Equal(t, http.StatusNotFound, lookRec.Code)
}
