package oidc_test

import (
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

func TestAuthorize_PKCERubberStamp(t *testing.T) {
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
