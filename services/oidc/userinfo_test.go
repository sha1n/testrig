package oidc_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig/services/oidc"
)

func bearerHeader(t *testing.T, target, token string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func TestUserinfo_HappyPath_ReturnsSub(t *testing.T) {
	iss := startMinimal(t)
	tok, err := iss.SignFor("alice", "test-api", time.Minute)
	if err != nil {
		t.Fatalf("SignFor: %v", err)
	}
	status, _, body := bearerHeader(t, iss.UserinfoURL(), tok)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["sub"] != "alice" {
		t.Errorf("sub = %v, want alice", resp["sub"])
	}
}

func TestUserinfo_WithCustomClaims_MergesIntoResponse(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost/cb").
		WithAllowedAudiences("test-api").
		WithUserClaim("alice", "email", "alice@example.com").
		WithUserClaim("alice", "name", "Alice")
	if _, err := iss.Start(context.Background(), slog.Default()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	tok, _ := iss.SignFor("alice", "test-api", time.Minute)
	status, _, body := bearerHeader(t, iss.UserinfoURL(), tok)
	if status != 200 {
		t.Fatalf("status = %d body = %s", status, body)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["email"] != "alice@example.com" {
		t.Errorf("email = %v", resp["email"])
	}
	if resp["name"] != "Alice" {
		t.Errorf("name = %v", resp["name"])
	}
}

func TestUserinfo_NoBearer_401_WWWAuthenticate(t *testing.T) {
	iss := startMinimal(t)
	status, headers, _ := bearerHeader(t, iss.UserinfoURL(), "")
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
	if !strings.HasPrefix(headers.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("WWW-Authenticate = %q", headers.Get("WWW-Authenticate"))
	}
}

func TestUserinfo_BadBearer_401_InvalidToken(t *testing.T) {
	iss := startMinimal(t)
	status, headers, _ := bearerHeader(t, iss.UserinfoURL(), "garbage.not.a.jwt")
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
	if !strings.Contains(headers.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate = %q", headers.Get("WWW-Authenticate"))
	}
}

func TestUserinfo_ExpiredToken_401_InvalidToken(t *testing.T) {
	iss := startMinimal(t)
	tok, err := iss.Sign(jwt.MapClaims{
		"iss": iss.IssuerURL(),
		"sub": "alice",
		"aud": "test-api",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, headers, _ := bearerHeader(t, iss.UserinfoURL(), tok)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
	if !strings.Contains(headers.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate = %q", headers.Get("WWW-Authenticate"))
	}
}

func TestUserinfo_WrongAudience_401_InvalidToken(t *testing.T) {
	iss := startMinimal(t)
	tok, _ := iss.SignFor("alice", "wrong-api", time.Minute)
	status, _, _ := bearerHeader(t, iss.UserinfoURL(), tok)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
}
