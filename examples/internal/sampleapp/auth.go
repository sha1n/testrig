package sampleapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// AuthConfig describes how the app validates incoming Bearer JWTs.
//
// KeyFunc resolves the RSA public key for a given token (typically by `kid`).
// In production it's typically built by github.com/MicahParks/keyfunc/v3
// against the issuer's JWKS endpoint; tests can build it the same way.
// Issuer and Audience are validated by the middleware against the token's
// `iss` and `aud` claims.
type AuthConfig struct {
	KeyFunc  jwt.Keyfunc
	Issuer   string
	Audience string
}

type ctxKey struct{}

// SubjectFromContext returns the authenticated subject extracted by authMiddleware
// from a validated Bearer JWT. The second return is false when the request
// did not flow through the middleware (e.g. unprotected routes).
func SubjectFromContext(ctx context.Context) (string, bool) {
	sub, ok := ctx.Value(ctxKey{}).(string)
	return sub, ok && sub != ""
}

// errorBody is the JSON envelope returned by writeUnauthorized.
type errorBody struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

// authMiddleware extracts a Bearer JWT from the Authorization header, validates
// it (RS256 + iss + aud + exp) using cfg, and on success injects the `sub`
// claim into the request context. Any failure short-circuits with 401.
func authMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, err := bearerToken(r)
			if err != nil {
				writeUnauthorized(w, err.Error())
				return
			}
			parsed, err := jwt.Parse(tok, cfg.KeyFunc,
				jwt.WithValidMethods([]string{"RS256"}),
				jwt.WithIssuer(cfg.Issuer),
				jwt.WithAudience(cfg.Audience),
				jwt.WithExpirationRequired(),
			)
			if err != nil {
				writeUnauthorized(w, "invalid token")
				return
			}
			claims, ok := parsed.Claims.(jwt.MapClaims)
			if !ok {
				writeUnauthorized(w, "invalid claims")
				return
			}
			sub, _ := claims["sub"].(string)
			if sub == "" {
				writeUnauthorized(w, "missing sub claim")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, sub))
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", errors.New("missing Authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", errors.New("expected Bearer scheme")
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if tok == "" {
		return "", errors.New("empty Bearer token")
	}
	return tok, nil
}

func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorBody{Error: "unauthorized", Reason: reason})
}
