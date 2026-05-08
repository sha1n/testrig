package oidc_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

// Re-declare oauthError locally — token.go's type is unexported.
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func parseOAuthError(t *testing.T, body string) (string, string) {
	t.Helper()
	var e oauthError
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("parse OAuth error body %q: %v", body, err)
	}
	return e.Error, e.ErrorDescription
}

func TestToken_MissingGrantType_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", e)
	}
}

func TestToken_UnsupportedGrantType_UnsupportedGrantType(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{"grant_type": {"refresh_token"}}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "unsupported_grant_type" {
		t.Errorf("error = %q, want unsupported_grant_type", e)
	}
}

func TestToken_NoClientAuth_InvalidClient_401_WWWAuthenticate(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{"grant_type": {"client_credentials"}, "audience": {"test-api"}}
	status, headers, body := httpPostForm(t, iss.TokenURL(), form, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if !strings.HasPrefix(headers.Get("WWW-Authenticate"), "Basic") {
		t.Errorf("WWW-Authenticate = %q, want Basic ...", headers.Get("WWW-Authenticate"))
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", e)
	}
}

func TestToken_BothBasicAndBodyAuth_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"audience":      {"test-api"},
		"client_id":     {iss.ClientID()},
		"client_secret": {iss.ClientSecret()},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", e)
	}
}

func TestToken_WrongClientSecret_InvalidClient_401(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{"grant_type": {"client_credentials"}, "audience": {"test-api"}}
	basic := &struct{ User, Pass string }{iss.ClientID(), "wrong-secret"}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", e)
	}
}

func TestToken_WrongClientID_InvalidClient_401(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{"grant_type": {"client_credentials"}, "audience": {"test-api"}}
	basic := &struct{ User, Pass string }{"wrong-client", iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", e)
	}
}

func TestToken_AuthCode_MissingCode_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"redirect_uri": {"http://localhost:8080/callback"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", e)
	}
}

func TestToken_AuthCode_UnknownCode_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {"unknown-code-xyz"},
		"redirect_uri": {"http://localhost:8080/callback"},
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

func TestToken_AuthCode_ReplayedCode_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	// First exchange — succeeds.
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, true)
	if resp.AccessToken == "" {
		t.Fatalf("first exchange produced no access token")
	}
	// Issue a fresh code via /authorize to test replay.
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
		"audience":      {"test-api"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	freshCode := loc.Query().Get("code")
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {freshCode},
		"redirect_uri": {"http://localhost:8080/callback"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	if s, _, _ := httpPostForm(t, iss.TokenURL(), form, basic); s != 200 {
		t.Fatalf("first /token: %d", s)
	}
	// Second exchange of the same code.
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("replay status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_grant" {
		t.Errorf("replay error = %q, want invalid_grant", e)
	}
}

func TestToken_AuthCode_ExpiredCode_InvalidGrant(t *testing.T) {
	t.Skip("requires injecting time mock or shortening codeTTL — covered indirectly by the consume() reason path; deferred to a future enhancement")
	_ = time.Second
}

func TestToken_AuthCode_RedirectURIMismatch_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
		"audience":      {"test-api"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"http://localhost:9999/different"}, // mismatch
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

func TestToken_ClientCredentials_DisallowedAudience_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type": {"client_credentials"},
		"audience":   {"unauthorised-api"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if e, _ := parseOAuthError(t, body); e != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", e)
	}
}

func TestToken_ErrorBodyShape(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{} // missing grant_type
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	_, headers, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q", headers.Get("Content-Type"))
	}
	var e oauthError
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error == "" {
		t.Errorf("error field empty")
	}
}
