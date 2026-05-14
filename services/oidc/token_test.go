package oidc_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/oidc"
)

type tokenResponse struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
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
	// auth_time MUST be present even when no audience was requested.
	// azp MUST be absent (per OIDC §2: only present when audience was requested).
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.IDToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse id_token: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	if _, ok := c["auth_time"]; !ok {
		t.Errorf("auth_time missing on no-audience id_token")
	}
	if _, ok := c["azp"]; ok {
		t.Errorf("azp unexpectedly present on no-audience id_token: %v", c["azp"])
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
	// RFC 9068 §2.2: jti REQUIRED on JWT access tokens.
	if jti, _ := c["jti"].(string); jti == "" {
		t.Errorf("jti missing or empty on auth_code access_token")
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
	// RFC 9068 §2.2: jti REQUIRED on JWT access tokens.
	if jti, _ := c["jti"].(string); jti == "" {
		t.Errorf("jti missing or empty on client_credentials access_token")
	}
}

func TestToken_ClientCredentials_Response_IncludesScope(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"grant_type": {"client_credentials"},
		"audience":   {"test-api"},
		"scope":      {"read"},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	_, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	var resp tokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Scope != "read" {
		t.Errorf("response scope = %q, want \"read\"", resp.Scope)
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
	form := url.Values{"grant_type": {"password"}}
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
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/callback").
		WithAllowedAudiences("test-api").
		WithCodeTTL(50 * time.Millisecond)
	if _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })

	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
		"audience":      {"test-api"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	time.Sleep(100 * time.Millisecond)

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
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

// TestToken_AuthCode_RedirectURIMismatch_Retry_Succeeds proves that a
// /token request with the wrong redirect_uri does NOT consume the code,
// so a subsequent retry with the correct value still succeeds. Per spec
// (and RFC 6749 §4.1.3), redirect_uri must be validated BEFORE the code
// is marked consumed.
func TestToken_AuthCode_RedirectURIMismatch_Retry_Succeeds(t *testing.T) {
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
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}

	// First /token: wrong redirect_uri → invalid_grant, code NOT consumed.
	wrongForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"http://localhost:9999/different"},
	}
	if s, _, _ := httpPostForm(t, iss.TokenURL(), wrongForm, basic); s != http.StatusBadRequest {
		t.Fatalf("first /token (mismatched redirect_uri): status = %d, want 400", s)
	}

	// Second /token: correct redirect_uri → success. The fix being tested:
	// the wrong-redirect_uri attempt above must NOT have invalidated the code.
	rightForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"http://localhost:8080/callback"},
	}
	status, _, body := httpPostForm(t, iss.TokenURL(), rightForm, basic)
	if status != 200 {
		t.Fatalf("retry /token (correct redirect_uri): status = %d body = %s, want 200", status, body)
	}
}

func TestToken_ParallelExchanges_NoStateCorruption(t *testing.T) {
	iss := startMinimal(t)
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]tokenResponse, n)
	subjects := make([]string, n)
	for k := 0; k < n; k++ {
		k := k
		subjects[k] = "user-" + string(rune('a'+k%26)) + string(rune('0'+k/26))
		go func() {
			defer wg.Done()
			results[k] = runAuthCode(t, iss, url.Values{
				"audience": {"test-api"},
				"sub":      {subjects[k]},
			}, true)
		}()
	}
	wg.Wait()
	parser := jwt.NewParser()
	for k, r := range results {
		if r.IDToken == "" || r.AccessToken == "" {
			t.Errorf("k=%d missing tokens", k)
			continue
		}
		parsed, _, err := parser.ParseUnverified(r.IDToken, jwt.MapClaims{})
		if err != nil {
			t.Errorf("k=%d parse: %v", k, err)
			continue
		}
		c := parsed.Claims.(jwt.MapClaims)
		if c["sub"] != subjects[k] {
			t.Errorf("k=%d sub=%v want %s", k, c["sub"], subjects[k])
		}
	}
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

func TestToken_IDToken_HasAuthTime(t *testing.T) {
	iss := startMinimal(t)
	before := time.Now().Unix()
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, true)
	after := time.Now().Unix()
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.IDToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse id_token: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	at, ok := c["auth_time"].(float64)
	if !ok {
		t.Fatalf("auth_time missing or wrong type: %v", c["auth_time"])
	}
	if int64(at) < before-1 || int64(at) > after+1 {
		t.Errorf("auth_time = %d outside [%d, %d]", int64(at), before, after)
	}
}

func TestToken_IDToken_HasAZP(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{"audience": {"test-api"}}, true)
	parser := jwt.NewParser()
	parsed, _, err := parser.ParseUnverified(resp.IDToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := parsed.Claims.(jwt.MapClaims)
	if c["azp"] != iss.ClientID() {
		t.Errorf("azp = %v, want client_id %v", c["azp"], iss.ClientID())
	}
}

func TestToken_Response_IncludesScope(t *testing.T) {
	iss := startMinimal(t)
	resp := runAuthCode(t, iss, url.Values{
		"audience": {"test-api"},
		"scope":    {"read write"},
	}, true)
	if resp.Scope != "read write" {
		t.Errorf("response scope = %q, want 'read write'", resp.Scope)
	}
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func TestPKCE_S256_VerifierMatches_Succeeds(t *testing.T) {
	iss := startMinimal(t)
	verifier := strings.Repeat("a", 64)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"audience":              {"test-api"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"code_verifier": {verifier},
	}
	basic := &struct{ User, Pass string }{iss.ClientID(), iss.ClientSecret()}
	status, _, body := httpPostForm(t, iss.TokenURL(), form, basic)
	if status != 200 {
		t.Fatalf("status = %d body = %s, want 200", status, body)
	}
}

func TestPKCE_S256_VerifierMismatches_InvalidGrant(t *testing.T) {
	iss := startMinimal(t)
	verifier := strings.Repeat("a", 64)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"audience":              {"test-api"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"code_verifier": {strings.Repeat("b", 64)},
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

func TestPKCE_VerifierMissing_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	verifier := strings.Repeat("a", 64)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"audience":              {"test-api"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
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

func TestPKCE_VerifierTooShort_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	verifier := strings.Repeat("a", 64)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"audience":              {"test-api"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"code_verifier": {strings.Repeat("a", 42)},
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

func TestPKCE_VerifierTooLong_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	verifier := strings.Repeat("a", 64)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"audience":              {"test-api"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	_, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	loc, _ := url.Parse(headers.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"code_verifier": {strings.Repeat("a", 129)},
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
