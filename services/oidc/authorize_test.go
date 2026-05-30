package oidc_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/sha1n/testrig/services/oidc"
)

// authorizeHappy issues a GET /authorize with valid params and returns the
// redirect Location URL parsed.
func authorizeHappy(t *testing.T, iss *oidc.Issuer, extras url.Values) *url.URL {
	t.Helper()
	q := url.Values{}
	q.Set("client_id", iss.ClientID())
	q.Set("redirect_uri", "http://localhost:8080/callback")
	q.Set("response_type", "code")
	for k, vs := range extras {
		q[k] = vs
	}
	target := iss.AuthorizationURL() + "?" + q.Encode()
	status, headers, body := httpGet(t, target)
	if status != 302 {
		t.Fatalf("status = %d (body=%s), want 302", status, body)
	}
	loc, err := url.Parse(headers.Get("Location"))
	if err != nil {
		t.Fatalf("Location parse: %v", err)
	}
	return loc
}

func TestAuthorize_Basic_RedirectsWithCode(t *testing.T) {
	iss := startMinimal(t)
	loc := authorizeHappy(t, iss, nil)
	if loc.Query().Get("code") == "" {
		t.Errorf("redirect missing 'code' query param")
	}
}

func TestAuthorize_StateEchoed(t *testing.T) {
	iss := startMinimal(t)
	loc := authorizeHappy(t, iss, url.Values{"state": []string{"xyz123"}})
	if loc.Query().Get("state") != "xyz123" {
		t.Errorf("state = %q, want xyz123", loc.Query().Get("state"))
	}
}

func TestAuthorize_NonceFlowsToIDToken(t *testing.T) {
	iss := startMinimal(t)
	loc := authorizeHappy(t, iss, url.Values{
		"nonce":    []string{"nonce-abc"},
		"audience": []string{"test-api"},
	})
	code := loc.Query().Get("code")
	// Full id_token verification happens in T10; here we only confirm the
	// /authorize step produces a code when nonce is present.
	if code == "" {
		t.Fatalf("missing code")
	}
}

func TestAuthorize_PKCE_AcceptedAtAuthorize(t *testing.T) {
	iss := startMinimal(t)
	loc := authorizeHappy(t, iss, url.Values{
		"code_challenge":        []string{"abc123"},
		"code_challenge_method": []string{"S256"},
	})
	if loc.Query().Get("code") == "" {
		t.Errorf("PKCE-equipped /authorize did not produce a code")
	}
}

func TestAuthorize_MultipleCodes_AreUnique(t *testing.T) {
	iss := startMinimal(t)
	c1 := authorizeHappy(t, iss, nil).Query().Get("code")
	c2 := authorizeHappy(t, iss, nil).Query().Get("code")
	if c1 == c2 {
		t.Errorf("expected distinct codes, got %q twice", c1)
	}
	if c1 == "" || c2 == "" {
		t.Errorf("empty codes: c1=%q c2=%q", c1, c2)
	}
}

// assertAuthorize400JSON checks the response is HTTP 400, no redirect, and
// the body parses as an OAuth JSON error with the expected error code.
func assertAuthorize400JSON(t *testing.T, target, wantError string) {
	t.Helper()
	status, headers, body := httpGet(t, target)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if loc := headers.Get("Location"); loc != "" {
		t.Errorf("unexpected redirect to %q", loc)
	}
	if got, _ := parseOAuthError(t, body); got != wantError {
		t.Errorf("error = %q, want %q", got, wantError)
	}
}

func TestAuthorize_MissingClientID_Returns400_NoRedirect(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
	}
	assertAuthorize400JSON(t, iss.AuthorizationURL()+"?"+q.Encode(), "invalid_client")
}

func TestAuthorize_WrongClientID_Returns400_NoRedirect(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {"not-the-real-client"},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
	}
	assertAuthorize400JSON(t, iss.AuthorizationURL()+"?"+q.Encode(), "invalid_client")
}

func TestAuthorize_MissingRedirectURI_Returns400_NoRedirect(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"response_type": {"code"},
	}
	assertAuthorize400JSON(t, iss.AuthorizationURL()+"?"+q.Encode(), "invalid_request")
}

func TestAuthorize_UnregisteredRedirectURI_Returns400_NoRedirect(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://evil.example/cb"},
		"response_type": {"code"},
	}
	assertAuthorize400JSON(t, iss.AuthorizationURL()+"?"+q.Encode(), "invalid_request")
}

func TestAuthorize_MissingResponseType_Redirects_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":    {iss.ClientID()},
		"redirect_uri": {"http://localhost:8080/callback"},
		"state":        {"S"},
	}
	status, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "S" {
		t.Errorf("state not echoed; got %q", loc.Query().Get("state"))
	}
}

func TestAuthorize_UnsupportedResponseType_Redirects_UnsupportedResponseType(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"token"},
		"state":         {"S2"},
	}
	status, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	if loc.Query().Get("error") != "unsupported_response_type" {
		t.Errorf("error = %q, want unsupported_response_type", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "S2" {
		t.Errorf("state not echoed; got %q", loc.Query().Get("state"))
	}
}

func TestAuthorize_DisallowedAudience_Redirects_InvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
		"audience":      {"not-allowed"},
	}
	status, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", loc.Query().Get("error"))
	}
}

func TestAuthorize_SubjectExtensionParam_OverridesDefault(t *testing.T) {
	iss := startMinimal(t)
	loc := authorizeHappy(t, iss, url.Values{
		"sub":      {"bob"},
		"audience": {"test-api"},
	})
	code := loc.Query().Get("code")
	// Verifying the resulting token's sub requires /token (Task 10).
	// For Task 9, just verify the request succeeded.
	if code == "" {
		t.Errorf("expected code, got empty")
	}
}

func TestAuthorize_POST_Succeeds(t *testing.T) {
	iss := startMinimal(t)
	form := url.Values{
		"client_id":     {iss.ClientID()},
		"redirect_uri":  {"http://localhost:8080/callback"},
		"response_type": {"code"},
	}
	status, headers, _ := httpPostForm(t, iss.AuthorizationURL(), form, nil)
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	if loc.Query().Get("code") == "" {
		t.Errorf("missing code in redirect")
	}
}

func TestAuthorize_PKCE_PlainMethod_RedirectsInvalidRequest(t *testing.T) {
	iss := startMinimal(t)
	q := url.Values{
		"client_id":             {iss.ClientID()},
		"redirect_uri":          {"http://localhost:8080/callback"},
		"response_type":         {"code"},
		"code_challenge":        {"some-challenge"},
		"code_challenge_method": {"plain"},
	}
	status, headers, _ := httpGet(t, iss.AuthorizationURL()+"?"+q.Encode())
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302", status)
	}
	loc, _ := url.Parse(headers.Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", loc.Query().Get("error"))
	}
}
