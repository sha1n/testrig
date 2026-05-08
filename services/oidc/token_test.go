package oidc_test

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig/services/oidc"
)

type tokenResponse struct {
	IDToken     string `json:"id_token,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	TokenType   string `json:"token_type,omitempty"`
	ExpiresIn   int    `json:"expires_in,omitempty"`
}

// runAuthCode performs a full /authorize → /token round-trip and returns the
// parsed token response. extras are extra /authorize query params; useBasicAuth
// chooses between Basic and body auth.
func runAuthCode(t *testing.T, iss *oidc.Issuer, extras url.Values, useBasicAuth bool) tokenResponse {
	t.Helper()
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
	}
	for k, v := range extras {
		q[k] = v
	}
	status, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	if status != 302 {
		t.Fatalf("/authorize status %d", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in /authorize redirect")
	}

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"http://localhost:8080/callback"},
	}
	var basic *struct{ User, Pass string }
	if useBasicAuth {
		basic = &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	} else {
		form.Set("client_id", iss.ClientID())
		form.Set("client_secret", iss.ClientSecret())
	}
	tStatus, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if tStatus != 200 {
		t.Fatalf("/token status %d body %s", tStatus, body)
	}
	var resp tokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal token response: %v body=%s", err, body)
	}
	return resp
}

func TestToken_AuthorizationCode_BasicAuth_ReturnsBothTokens(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, true)
	if resp.IDToken == "" {
		t.Errorf("id_token missing")
	}
	if resp.AccessToken == "" {
		t.Errorf("access_token missing")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expires_in = %d, want > 0", resp.ExpiresIn)
	}
}

func TestToken_AuthorizationCode_BodyAuth_ReturnsBothTokens(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, false)
	if resp.IDToken == "" || resp.AccessToken == "" {
		t.Errorf("missing tokens: id=%q access=%q", resp.IDToken, resp.AccessToken)
	}
}

func TestToken_ClientCredentials_AccessTokenOnly(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type": {"client_credentials"},
		"audience":   {"test-api"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != 200 {
		t.Fatalf("status %d body %s", status, body)
	}
	var resp tokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken == "" {
		t.Errorf("access_token missing")
	}
	if resp.IDToken != "" {
		t.Errorf("id_token unexpectedly present: %s", resp.IDToken)
	}
}

func TestToken_AuthorizationCode_NoAudience_IDTokenOnly(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, nil, true)
	if resp.IDToken == "" {
		t.Errorf("id_token missing")
	}
	if resp.AccessToken != "" {
		t.Errorf("access_token unexpectedly present: %s", resp.AccessToken)
	}
}

func TestToken_IDTokenClaims(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{
		"audience": {"test-api"},
		"nonce":    {"n-abc"},
	}, true)
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.IDToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse id_token: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	if c["iss"] != iss.IssuerURL() {
		t.Errorf("iss = %v", c["iss"])
	}
	if c["sub"] != "test-user" {
		t.Errorf("sub = %v, want test-user", c["sub"])
	}
	if c["aud"] != iss.ClientID() {
		t.Errorf("id_token aud = %v, want client_id %v", c["aud"], iss.ClientID())
	}
	if c["nonce"] != "n-abc" {
		t.Errorf("nonce = %v, want n-abc", c["nonce"])
	}
}

func TestToken_AccessTokenClaims_AuthCode(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{
		"audience": {"test-api"},
		"scope":    {"read write"},
	}, true)
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.AccessToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse access_token: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	if c["aud"] != "test-api" {
		t.Errorf("access_token aud = %v, want test-api", c["aud"])
	}
	if c["sub"] != "test-user" {
		t.Errorf("sub = %v, want test-user", c["sub"])
	}
	if c["scope"] != "read write" {
		t.Errorf("scope = %v, want 'read write'", c["scope"])
	}
}

func TestToken_AccessTokenClaims_ClientCredentials(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type": {"client_credentials"},
		"audience":   {"test-api"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	_, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	var resp tokenResponse
	_ = json.Unmarshal([]byte(body), &resp)
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.AccessToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	if c["sub"] != iss.ClientID() {
		t.Errorf("sub = %v, want client_id %v", c["sub"], iss.ClientID())
	}
	if c["aud"] != "test-api" {
		t.Errorf("aud = %v, want test-api", c["aud"])
	}
}

func TestToken_TokensValidateViaJWKS(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, true)
	pub := iss.PublicKey()
	for label, tok := range map[string]string{"id_token": resp.IDToken, "access_token": resp.AccessToken} {
		parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return pub, nil })
		if err != nil || !parsed.Valid {
			t.Errorf("%s does not validate: %v", label, err)
		}
	}
}
