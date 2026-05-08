package oidc

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// handleUserinfo serves GET /userinfo per OIDC Core §5.3. Validates the
// Bearer token's signature, expiry, issuer, and audience, then returns the
// configured userClaims for the token's `sub` plus `{sub}` itself.
func (i *Issuer) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="oidc-test-issuer"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rawToken := strings.TrimPrefix(auth, "Bearer ")

	parsed, err := jwt.Parse(rawToken, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, errors.New("unexpected signing method")
		}
		return i.PublicKey(), nil
	})
	if err != nil || !parsed.Valid {
		writeBearerError(w, "invalid_token", "token validation failed")
		return
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		writeBearerError(w, "invalid_token", "claims malformed")
		return
	}
	if iss, _ := claims["iss"].(string); iss != i.IssuerURL() {
		writeBearerError(w, "invalid_token", "iss mismatch")
		return
	}
	// Audience: jwt.Parse does not validate audience by default in v5
	// (we'd need jwt.WithAudience(...), and it only takes a single value).
	// We validate manually against the allowed-list. claims.GetAudience()
	// handles both single-string and array-form aud claims robustly.
	auds, err := claims.GetAudience()
	if err != nil || !slices.ContainsFunc(auds, func(a string) bool { return slices.Contains(i.allowedAudiences, a) }) {
		writeBearerError(w, "invalid_token", "aud not allowed")
		return
	}

	sub, _ := claims["sub"].(string)
	resp := map[string]any{"sub": sub}
	if extra, ok := i.userClaims[sub]; ok {
		for k, v := range extra {
			if k == "sub" {
				continue
			}
			resp[k] = v
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeBearerError(w http.ResponseWriter, code, desc string) {
	w.Header().Set("WWW-Authenticate", `Bearer error="`+code+`", error_description="`+desc+`"`)
	w.WriteHeader(http.StatusUnauthorized)
}
