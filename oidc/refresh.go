package oidc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// refreshRecord captures the state needed to mint a fresh access token from
// a refresh token exchange.
type refreshRecord struct {
	subject  string
	audience string
	scope    string
	clientID string
	expires  time.Time
}

// refreshStore is the single-use, expiry-bounded refresh token store. The
// jti claim of each issued JWT is the store key.
type refreshStore struct {
	mu      sync.Mutex
	records map[string]*refreshRecord
}

func newRefreshStore() *refreshStore {
	return &refreshStore{records: make(map[string]*refreshRecord)}
}

// issueRefreshToken mints a JWT-formatted refresh token and stores rec keyed
// by jti. Returns the signed token string.
func (i *Issuer) issueRefreshToken(rec *refreshRecord) (string, error) {
	jtiBuf := make([]byte, 16)
	if _, err := rand.Read(jtiBuf); err != nil {
		return "", err
	}
	jti := hex.EncodeToString(jtiBuf)
	now := time.Now()
	rec.expires = now.Add(i.refreshTokenTTL)
	tok, err := i.signClaims(jwt.MapClaims{
		"iss":       i.IssuerURL(),
		"sub":       rec.subject,
		"aud":       i.IssuerURL() + "/refresh", // distinguishes refresh from access tokens
		"client_id": rec.clientID,
		"scope":     rec.scope,
		"jti":       jti,
		"iat":       now.Unix(),
		"exp":       rec.expires.Unix(),
	})
	if err != nil {
		return "", err
	}
	i.refreshStore.mu.Lock()
	i.refreshStore.records[jti] = rec
	i.refreshStore.mu.Unlock()
	return tok, nil
}

// consumeRefreshToken validates and atomically removes the refresh token.
// Returns the stored record on success, or (nil, reason) where reason maps
// to invalid_grant per RFC 6749 §5.2.
func (i *Issuer) consumeRefreshToken(tokenStr string) (*refreshRecord, string) {
	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, errors.New("unexpected signing method")
		}
		return i.PublicKey(), nil
	})
	if err != nil || !parsed.Valid {
		return nil, "invalid_signature"
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, "malformed_claims"
	}
	if aud, _ := claims["aud"].(string); aud != i.IssuerURL()+"/refresh" {
		return nil, "wrong_audience"
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil, "missing_jti"
	}
	i.refreshStore.mu.Lock()
	defer i.refreshStore.mu.Unlock()
	rec, ok := i.refreshStore.records[jti]
	if !ok {
		return nil, "not_found"
	}
	if time.Now().After(rec.expires) {
		delete(i.refreshStore.records, jti)
		return nil, "expired"
	}
	delete(i.refreshStore.records, jti)
	return rec, "ok"
}

// handleRefreshGrant implements grant_type=refresh_token. Authentication
// already happened in handleToken.
func (i *Issuer) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.PostForm.Get("refresh_token")
	if tokenStr == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	rec, reason := i.consumeRefreshToken(tokenStr)
	if reason != "ok" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh_token "+reason)
		return
	}
	// Single-client fixture: rec.clientID is always i.clientID since this
	// issuer issued the original record. The check below is defensive and
	// would matter in a multi-client extension; left in place to make the
	// invariant explicit and to simplify a future multi-client refactor.
	if rec.clientID != i.clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh_token client_id mismatch")
		return
	}

	now := time.Now()
	exp := now.Add(i.tokenTTL)

	// Rotate: issue a fresh refresh token regardless of grant shape.
	newRefresh, err := i.issueRefreshToken(&refreshRecord{
		subject:  rec.subject,
		audience: rec.audience,
		scope:    rec.scope,
		clientID: rec.clientID,
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	resp := map[string]any{
		"refresh_token": newRefresh,
		"token_type":    "Bearer",
		"expires_in":    i.expiresIn(),
	}
	if rec.scope != "" {
		resp["scope"] = rec.scope
	}

	// Mirror handleAuthCodeGrant: only mint an access token if the original
	// flow registered an audience. An id-token-only flow that requested
	// offline_access still rotates its refresh token but produces no access
	// token, matching the original grant's contract.
	if rec.audience != "" {
		jtiBuf := make([]byte, 16)
		if _, err := rand.Read(jtiBuf); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		accessClaims := jwt.MapClaims{
			"iss": i.IssuerURL(),
			"sub": rec.subject,
			"aud": rec.audience,
			"iat": now.Unix(),
			"exp": exp.Unix(),
			"jti": hex.EncodeToString(jtiBuf),
		}
		if rec.scope != "" {
			accessClaims["scope"] = rec.scope
		}
		accessTok, err := i.signClaims(accessClaims)
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		resp["access_token"] = accessTok
	}

	writeTokenResponse(w, resp)
}

// scopeContainsOfflineAccess reports whether the space-separated scope string
// includes "offline_access".
func scopeContainsOfflineAccess(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == "offline_access" {
			return true
		}
	}
	return false
}
