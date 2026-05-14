package sampleapp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sha1n/testrig/examples/internal/sampleapp"
)

const (
	testIssuer   = "https://test-issuer.example"
	testAudience = "test-api"
	testKID      = "test-kid"
)

// authFixture builds a self-contained auth setup: an RSA key, a Keyfunc that
// returns the matching public key, and helpers to mint tokens.
type authFixture struct {
	priv     *rsa.PrivateKey
	cfg      sampleapp.AuthConfig
	subject  string
	audience string
	issuer   string
	kid      string
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	fix := &authFixture{
		priv:     priv,
		subject:  "alice",
		audience: testAudience,
		issuer:   testIssuer,
		kid:      testKID,
	}
	fix.cfg = sampleapp.AuthConfig{
		KeyFunc: func(tok *jwt.Token) (any, error) {
			if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return &fix.priv.PublicKey, nil
		},
		Issuer:   testIssuer,
		Audience: testAudience,
	}
	return fix
}

func (f *authFixture) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	signed, err := tok.SignedString(f.priv)
	require.NoError(t, err)
	return signed
}

func (f *authFixture) validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub": f.subject,
		"iss": f.issuer,
		"aud": f.audience,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

// wrapAuth uses a sampleapp.Server to exercise the middleware indirectly via
// the public Handler() — verifying the middleware is wired into protected
// routes. The handler under test is a small inner mux that we point at via
// /save (a protected route).
//
// To keep these tests focused on auth behaviour without DB plumbing, we build
// a Server with a nil DB and rely on the middleware short-circuiting before
// any handler logic runs. When auth succeeds (the happy-path case), we use
// the dedicated /save endpoint's 400-on-missing-key branch as the signal
// that the request flowed through the middleware.
func makeProtectedServer(t *testing.T, cfg sampleapp.AuthConfig) http.Handler {
	t.Helper()
	srv := sampleapp.New(nil, sampleapp.Config{
		RemoteURL: "http://irrelevant",
		Auth:      cfg,
	})
	return srv.Handler()
}

func TestAuth_ValidToken_RequestReachesHandler(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)

	tok := fix.sign(t, fix.validClaims())
	req := httptest.NewRequest(http.MethodPost, "/save", nil) // no key — expect 400
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Reached the handler (which then 400'd on missing key).
	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"expected 400 (missing key) — proves middleware passed; got %d", rec.Code)
}

func TestAuth_MissingHeader_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/save?key=k", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
}

func TestAuth_WrongScheme_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_EmptyBearer_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAuth_WhitespaceOnlyBearer_Returns401 covers the trim-result-empty
// branch in bearerToken — "Bearer    " (header is non-empty, has the right
// prefix, but contains only whitespace after the scheme).
func TestAuth_WhitespaceOnlyBearer_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer \t  \t")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_BadSignature_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	tok := fix.sign(t, fix.validClaims())
	// Replace the whole signature segment with an obviously invalid value.
	dot := strings.LastIndexByte(tok, '.')
	require.Greater(t, dot, 0)
	tampered := tok[:dot+1] + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_ExpiredToken_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	claims := fix.validClaims()
	claims["exp"] = time.Now().Add(-time.Minute).Unix()
	tok := fix.sign(t, claims)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_WrongIssuer_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	claims := fix.validClaims()
	claims["iss"] = "https://other-issuer.example"
	tok := fix.sign(t, claims)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_WrongAudience_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	claims := fix.validClaims()
	claims["aud"] = "wrong-api"
	tok := fix.sign(t, claims)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_MissingSub_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	claims := fix.validClaims()
	delete(claims, "sub")
	tok := fix.sign(t, claims)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuth_HS256_Returns401(t *testing.T) {
	fix := newAuthFixture(t)
	h := makeProtectedServer(t, fix.cfg)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, fix.validClaims())
	signed, err := tok.SignedString([]byte("attacker-secret"))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/save?key=k", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSubjectFromContext_NotSet(t *testing.T) {
	_, ok := sampleapp.SubjectFromContext(context.Background())
	assert.False(t, ok)
}
