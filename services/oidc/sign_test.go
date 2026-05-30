package oidc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig/services/oidc"
)

func TestSign_BeforeStart_ReturnsError(t *testing.T) {
	iss := oidc.New("idp")
	_, err := iss.Sign(jwt.MapClaims{"sub": "x"})
	if err == nil || !strings.Contains(err.Error(), "not started") {
		t.Errorf("expected 'not started' error, got %v", err)
	}
}

func TestSign_AutoFillsAbsentClaims(t *testing.T) {
	iss := startMinimal(t)
	tok, err := iss.Sign(jwt.MapClaims{"sub": "alice"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return iss.PublicKey(), nil })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != iss.IssuerURL() {
		t.Errorf("iss = %v, want %s", claims["iss"], iss.IssuerURL())
	}
	if _, ok := claims["iat"]; !ok {
		t.Errorf("iat missing, want auto-filled")
	}
	if parsed.Header["kid"] == nil {
		t.Errorf("kid header missing")
	}
}

func TestSign_DoesNotOverrideCallerClaims(t *testing.T) {
	iss := startMinimal(t)
	tok, err := iss.Sign(jwt.MapClaims{
		"sub": "alice",
		"iss": "https://wrong-issuer.example",
		"iat": float64(1234567890),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Parse without validation (we want to inspect, not verify, since iss is wrong).
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "https://wrong-issuer.example" {
		t.Errorf("iss was overwritten to %v, expected caller value preserved", claims["iss"])
	}
	if claims["iat"].(float64) != 1234567890 {
		t.Errorf("iat was overwritten to %v, expected 1234567890", claims["iat"])
	}
}

func TestSignFor_ValidatesInputs(t *testing.T) {
	iss := startMinimal(t)
	cases := []struct {
		name            string
		sub, aud        string
		ttl             time.Duration
		wantErrContains string
	}{
		{"empty subject", "", "api", time.Minute, "subject must not be empty"},
		{"empty audience", "alice", "", time.Minute, "audience must not be empty"},
		{"zero ttl", "alice", "api", 0, "ttl must be > 0"},
		{"negative ttl", "alice", "api", -time.Second, "ttl must be > 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := iss.SignFor(tc.sub, tc.aud, tc.ttl)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("err=%v, want substring %q", err, tc.wantErrContains)
			}
		})
	}
}

func TestSignFor_ResultingTokenVerifiesViaPublicKey(t *testing.T) {
	iss := startMinimal(t)
	tok, err := iss.SignFor("alice", "test-api", time.Minute)
	if err != nil {
		t.Fatalf("SignFor: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return iss.PublicKey(), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("validation failed: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != "alice" {
		t.Errorf("sub = %v, want alice", claims["sub"])
	}
	if claims["aud"] != "test-api" {
		t.Errorf("aud = %v, want test-api", claims["aud"])
	}
}
