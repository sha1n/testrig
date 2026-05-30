// Package sampletest provides a reusable integration-test harness for the
// example apps. Both koanf-app and viper-app share an identical end-to-end
// surface; this package owns the scenarios so each app's server_test.go is
// a thin per-app TestMain plus named-test delegators.
package sampletest

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wiremock/go-wiremock"

	"github.com/sha1n/testrig/services/oidc"
)

// Harness bundles the runtime state each app's server_test.go needs to drive
// the integration scenarios. Build one in TestMain after testenv.Setup; pass
// to the scenario methods from named Test* funcs.
type Harness struct {
	DB         *sql.DB
	Handler    http.Handler
	Issuer     *oidc.Issuer
	WireMock   *wiremock.Client
	Audience   string
	UserAlice  string
	UserBob    string
	TokenTTL   time.Duration
	DBPollWait time.Duration
}

// New constructs a Harness with sensible defaults for the per-user subjects
// ("alice", "bob"), token TTL (1h), and DB poll budget (10s).
func New(db *sql.DB, handler http.Handler, issuer *oidc.Issuer, wm *wiremock.Client, audience string) *Harness {
	return &Harness{
		DB:         db,
		Handler:    handler,
		Issuer:     issuer,
		WireMock:   wm,
		Audience:   audience,
		UserAlice:  "alice",
		UserBob:    "bob",
		TokenTTL:   time.Hour,
		DBPollWait: 10 * time.Second,
	}
}

// TokenFor mints a valid Bearer token for the given subject.
func (h *Harness) TokenFor(t *testing.T, subject string) string {
	t.Helper()
	tok, err := h.Issuer.SignFor(subject, h.Audience, h.TokenTTL)
	require.NoError(t, err)
	return tok
}

// resetWireMock removes all stubs so each scenario starts from a clean slate.
func (h *Harness) resetWireMock(t *testing.T) {
	t.Helper()
	require.NoError(t, h.WireMock.Reset())
}

// stubLookup configures WireMock to return body for GET /lookup?key=<key>.
func (h *Harness) stubLookup(t *testing.T, key, body string) {
	t.Helper()
	require.NoError(t, h.WireMock.StubFor(
		wiremock.Get(wiremock.URLPathEqualTo("/lookup")).
			WithQueryParam("key", wiremock.EqualTo(key)).
			WillReturnResponse(wiremock.NewResponse().
				WithStatus(http.StatusOK).
				WithHeaders(map[string]string{"Content-Type": "application/json"}).
				WithBody(body)),
	))
}

// withServer runs fn against a freshly started httptest.Server wrapping the
// shared handler so each scenario gets a real listening port.
func (h *Harness) withServer(t *testing.T, fn func(srvURL string)) {
	t.Helper()
	srv := httptest.NewServer(h.Handler)
	defer srv.Close()
	fn(srv.URL)
}

func authPost(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func authGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// SaveAndLookupHappyPath: end-to-end authenticated flow.
//
//	alice's token → POST /save?key=k1 → server fetches WireMock-stubbed body
//	             → row appears in DB scoped to (alice, k1)
//	             → GET /lookup?key=k1 → 200 + stubbed body
func (h *Harness) SaveAndLookupHappyPath(t *testing.T) {
	h.resetWireMock(t)
	const key = "alpha"
	const body = `{"data":"alpha-value"}`
	h.stubLookup(t, key, body)
	token := h.TokenFor(t, h.UserAlice)

	h.withServer(t, func(srvURL string) {
		resp := authPost(t, srvURL+"/save?key="+key, token)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode)

		require.Eventually(t, func() bool {
			var got string
			err := h.DB.QueryRow(`SELECT response FROM responses WHERE user_id = $1 AND key = $2`,
				h.UserAlice, key).Scan(&got)
			return err == nil && got == body
		}, h.DBPollWait, 10*time.Millisecond, "row for (user=%s key=%s) did not appear", h.UserAlice, key)

		lookup := authGet(t, srvURL+"/lookup?key="+key, token)
		defer func() { _ = lookup.Body.Close() }()
		assert.Equal(t, http.StatusOK, lookup.StatusCode)
		read, _ := io.ReadAll(lookup.Body)
		assert.Equal(t, body, string(read))
	})
}

// MissingToken: no Authorization header → 401.
func (h *Harness) MissingToken(t *testing.T) {
	h.withServer(t, func(srvURL string) {
		resp, err := http.Post(srvURL+"/save?key=k", "", nil)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// ExpiredToken: token with exp in the past → 401.
func (h *Harness) ExpiredToken(t *testing.T) {
	tok, err := h.Issuer.Sign(jwt.MapClaims{
		"sub": h.UserAlice,
		"aud": h.Audience,
		"iss": h.Issuer.IssuerURL(),
		"iat": time.Now().Add(-2 * time.Minute).Unix(),
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	require.NoError(t, err)
	h.withServer(t, func(srvURL string) {
		resp := authPost(t, srvURL+"/save?key=k", tok)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// WrongAudience: token with aud != configured audience → 401.
func (h *Harness) WrongAudience(t *testing.T) {
	tok, err := h.Issuer.SignFor(h.UserAlice, "other-api", h.TokenTTL)
	require.NoError(t, err)
	h.withServer(t, func(srvURL string) {
		resp := authPost(t, srvURL+"/save?key=k", tok)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// BadSignature: a valid token with its signature segment overwritten by an
// unrelated valid base64url payload → 401.
func (h *Harness) BadSignature(t *testing.T) {
	tok := h.TokenFor(t, h.UserAlice)
	// Token shape: header.payload.signature. Replace signature wholesale with
	// a clearly invalid (but well-formed-looking) value so the verifier always
	// rejects it regardless of what the original last byte happened to be.
	dot := strings.LastIndexByte(tok, '.')
	require.Greater(t, dot, 0)
	tampered := tok[:dot+1] + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	h.withServer(t, func(srvURL string) {
		resp := authPost(t, srvURL+"/save?key=k", tampered)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// PerUserIsolation: alice saves under key K; bob looks up the same key and
// gets 404 — proving rows are scoped per-user.
func (h *Harness) PerUserIsolation(t *testing.T) {
	h.resetWireMock(t)
	const key = "isolation-key"
	const body = `{"owner":"alice"}`
	h.stubLookup(t, key, body)
	aliceTok := h.TokenFor(t, h.UserAlice)
	bobTok := h.TokenFor(t, h.UserBob)

	h.withServer(t, func(srvURL string) {
		resp := authPost(t, srvURL+"/save?key="+key, aliceTok)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode)

		require.Eventually(t, func() bool {
			var got string
			err := h.DB.QueryRow(`SELECT response FROM responses WHERE user_id = $1 AND key = $2`,
				h.UserAlice, key).Scan(&got)
			return err == nil && got == body
		}, h.DBPollWait, 10*time.Millisecond)

		bobLook := authGet(t, srvURL+"/lookup?key="+key, bobTok)
		_ = bobLook.Body.Close()
		assert.Equal(t, http.StatusNotFound, bobLook.StatusCode,
			"bob must not see alice's row under shared key %q", key)

		aliceLook := authGet(t, srvURL+"/lookup?key="+key, aliceTok)
		defer func() { _ = aliceLook.Body.Close() }()
		assert.Equal(t, http.StatusOK, aliceLook.StatusCode)
	})
}

// SchemaSeedApplied: the in-process SchemaSeed service published its marker.
func (h *Harness) SchemaSeedApplied(t *testing.T, props map[string]string) {
	if props["seed.applied"] != "true" {
		t.Errorf("expected seed.applied=true, got %q", props["seed.applied"])
	}
}
