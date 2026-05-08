package oidc

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// oauthError represents an RFC 6749 §5.2 error response body.
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="oidc-test-issuer"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oauthError{Error: code, ErrorDescription: desc})
}

// writeTokenResponse marshals a successful /token JSON response with the
// RFC 6749 §5.1-mandated Cache-Control: no-store header.
func writeTokenResponse(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(payload)
}

// authenticateClient enforces "exactly one of" Basic OR body, validates the
// credentials match the registered client. Returns true on success.
func (i *Issuer) authenticateClient(r *http.Request) (ok bool, errorCode, desc string, status int) {
	user, pass, hasBasic := r.BasicAuth()
	// RFC 6749 §2.3.1: credentials must travel in the request body, not the
	// URL query string. r.PostForm.Get reads body only; r.FormValue would
	// also accept query-string credentials.
	bodyID := r.PostForm.Get("client_id")
	bodySecret := r.PostForm.Get("client_secret")
	hasBody := bodyID != "" || bodySecret != ""

	if hasBasic && hasBody {
		return false, "invalid_request", "both Basic and body client authentication provided", http.StatusBadRequest
	}
	if !hasBasic && !hasBody {
		return false, "invalid_client", "client authentication required", http.StatusUnauthorized
	}
	clientID, clientSecret := bodyID, bodySecret
	if hasBasic {
		clientID, clientSecret = user, pass
	}
	if clientID != i.clientID || clientSecret != i.clientSecret {
		return false, "invalid_client", "invalid client credentials", http.StatusUnauthorized
	}
	return true, "", "", 0
}

// handleToken implements POST /token. Dispatches by grant_type; mints tokens.
func (i *Issuer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	grantType := r.PostForm.Get("grant_type")
	if grantType == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
		return
	}
	if grantType != "authorization_code" && grantType != "client_credentials" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or client_credentials")
		return
	}

	if ok, errCode, desc, status := i.authenticateClient(r); !ok {
		writeOAuthError(w, status, errCode, desc)
		return
	}

	switch grantType {
	case "authorization_code":
		i.handleAuthCodeGrant(w, r)
	case "client_credentials":
		i.handleClientCredentialsGrant(w, r)
	}
}

func (i *Issuer) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	if code == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}
	if redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return
	}

	rec, reason := i.codeStore.consume(code)
	if reason != "ok" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code "+reason)
		return
	}
	if rec.redirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}

	now := time.Now()
	exp := now.Add(i.tokenTTL)

	idClaims := jwt.MapClaims{
		"iss": i.IssuerURL(),
		"sub": rec.subject,
		"aud": i.clientID, // ID token aud is always client_id (OIDC standard)
		"iat": now.Unix(),
		"exp": exp.Unix(),
	}
	if rec.nonce != "" {
		idClaims["nonce"] = rec.nonce
	}
	idTok, err := i.signClaims(idClaims)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	resp := map[string]any{
		"id_token":   idTok,
		"token_type": "Bearer",
		"expires_in": int(i.tokenTTL.Seconds()),
	}

	if rec.audience != "" {
		accessClaims := jwt.MapClaims{
			"iss": i.IssuerURL(),
			"sub": rec.subject,
			"aud": rec.audience,
			"iat": now.Unix(),
			"exp": exp.Unix(),
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

func (i *Issuer) handleClientCredentialsGrant(w http.ResponseWriter, r *http.Request) {
	audience := r.PostForm.Get("audience")
	if audience == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "audience is required for client_credentials")
		return
	}
	if !slices.Contains(i.allowedAudiences, audience) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "audience not allowed")
		return
	}

	now := time.Now()
	exp := now.Add(i.tokenTTL)
	claims := jwt.MapClaims{
		"iss": i.IssuerURL(),
		"sub": i.clientID, // Auth0 convention for client_credentials
		"aud": audience,
		"iat": now.Unix(),
		"exp": exp.Unix(),
	}
	if scope := r.PostForm.Get("scope"); scope != "" {
		claims["scope"] = scope
	}
	tok, err := i.signClaims(claims)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeTokenResponse(w, map[string]any{
		"access_token": tok,
		"token_type":   "Bearer",
		"expires_in":   int(i.tokenTTL.Seconds()),
	})
}
