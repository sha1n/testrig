package oidc_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig/oidc"
)

func runAuthCodeWithScope(t *testing.T, iss *oidc.Issuer, scope string) tokenResponse {
	t.Helper()
	return runAuthCode(t, iss, url.Values{
		"audience": {"test-api"},
		"scope":    {scope},
	}, true)
}

func TestRefreshToken_IssuedWhen_OfflineAccessRequested(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCodeWithScope(t, iss, "read offline_access")
	if resp.RefreshToken == "" {
		t.Errorf("refresh_token missing despite offline_access scope")
	}
}

func TestRefreshToken_NotIssued_WithoutOfflineAccess(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCodeWithScope(t, iss, "read write")
	if resp.RefreshToken != "" {
		t.Errorf("refresh_token unexpectedly issued: %s", resp.RefreshToken)
	}
}

func TestRefreshToken_Exchange_ReturnsNewAccessToken(t *testing.T) {
	iss := startMinimal(t)
	first := runAuthCodeWithScope(t, iss, "read offline_access")
	if first.RefreshToken == "" {
		t.Fatalf("no refresh_token issued")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {first.RefreshToken},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != 200 {
		t.Fatalf("status = %d body = %s", status, body)
	}
	var resp tokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken == "" {
		t.Errorf("no access_token in refresh response")
	}
	if resp.AccessToken == first.AccessToken {
		t.Errorf("expected fresh access_token, got the same one")
	}
	if resp.RefreshToken == "" {
		t.Errorf("no refresh_token in refresh response (rotation should produce a new one)")
	}
	if resp.RefreshToken == first.RefreshToken {
		t.Errorf("refresh_token was not rotated (got same as original)")
	}
	parsed, _, _ := jwt.NewParser().ParseUnverified(resp.AccessToken, jwt.MapClaims{})
	c := parsed.Claims.(jwt.MapClaims)
	if c["sub"] != "test-user" {
		t.Errorf("sub = %v", c["sub"])
	}
	if c["aud"] != "test-api" {
		t.Errorf("aud = %v", c["aud"])
	}
}

func TestRefreshToken_Rotation_OldTokenRejected(t *testing.T) {
	iss := startMinimal(t)
	first := runAuthCodeWithScope(t, iss, "read offline_access")
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {first.RefreshToken},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	if s, _, _ := httpPostForm(t, iss.TokenURL(), form, basic); s != 200 {
		t.Fatalf("first refresh: %d", s)
	}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", e)
	}
}

// TestRefreshToken_TamperedJTI_InvalidGrant fabricates a refresh-shaped JWT
// with a synthetic jti not present in the store. consumeRefreshToken's lookup
// fails (not_found) before reaching the client_id check. In a single-client
// fixture the rec.clientID-mismatch branch is unreachable through the public
// API; this test covers the upstream fabricated-token rejection path. A
// genuine wrong-client scenario would require multi-client support.
func TestRefreshToken_TamperedJTI_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	// Fabricate a refresh-shaped JWT signed by the issuer but with a synthetic jti.
	// The signature is valid, the audience matches, but the JTI is unknown.
	tampered, err := iss.Sign(jwt.MapClaims{
		"iss":       iss.IssuerURL(),
		"sub":       "test-user",
		"aud":       iss.IssuerURL() + "/refresh",
		"client_id": "other-client",
		"scope":     "read offline_access",
		"jti":       "tampered-jti",
		"iat":       0,
		"exp":       9999999999,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tampered},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", e)
	}
}

func TestRefreshToken_Missing_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{"grant_type": {"refresh_token"}}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", e)
	}
}

func TestRefreshToken_Malformed_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"not.a.valid.jwt"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", e)
	}
}
