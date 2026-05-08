package oidc_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type discoveryDoc struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
	SubjectTypesSupported             []string `json:"subject_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

func TestDiscovery_HTTP200_JSONContentType(t *testing.T) {
	iss := startMinimal(t)
	status, headers, body := httpGet(t, iss.DiscoveryURL())
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", headers.Get("Content-Type"))
	}
	if body == "" {
		t.Errorf("empty body")
	}
}

func TestDiscovery_FieldsMatchAccessors(t *testing.T) {
	iss := startMinimal(t)
	_, _, body := httpGet(t, iss.DiscoveryURL())
	var d discoveryDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Issuer != iss.IssuerURL() {
		t.Errorf("issuer = %q, want %q", d.Issuer, iss.IssuerURL())
	}
	if d.AuthorizationEndpoint != iss.AuthorizationURL() {
		t.Errorf("authorization_endpoint = %q, want %q", d.AuthorizationEndpoint, iss.AuthorizationURL())
	}
	if d.TokenEndpoint != iss.TokenURL() {
		t.Errorf("token_endpoint = %q, want %q", d.TokenEndpoint, iss.TokenURL())
	}
	if d.JWKSURI != iss.JWKSURL() {
		t.Errorf("jwks_uri = %q, want %q", d.JWKSURI, iss.JWKSURL())
	}
}

func TestDiscovery_AdvertisedAlgsAndGrants(t *testing.T) {
	iss := startMinimal(t)
	_, _, body := httpGet(t, iss.DiscoveryURL())
	var d discoveryDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	containsAll(t, "response_types_supported", d.ResponseTypesSupported, "code")
	containsAll(t, "grant_types_supported", d.GrantTypesSupported, "authorization_code", "client_credentials")
	containsAll(t, "id_token_signing_alg_values_supported", d.IDTokenSigningAlgValuesSupported, "RS256")
	containsAll(t, "subject_types_supported", d.SubjectTypesSupported, "public")
	containsAll(t, "token_endpoint_auth_methods_supported", d.TokenEndpointAuthMethodsSupported, "client_secret_basic", "client_secret_post")
	containsAll(t, "code_challenge_methods_supported", d.CodeChallengeMethodsSupported, "S256")
}

func TestDiscovery_NoFalseAdvertising(t *testing.T) {
	iss := startMinimal(t)
	_, _, body := httpGet(t, iss.DiscoveryURL())
	// We deliberately do not advertise userinfo_endpoint, claims_supported,
	// scopes_supported, etc. since the implementation does not enforce them.
	for _, banned := range []string{`"userinfo_endpoint"`, `"claims_supported"`, `"scopes_supported"`, `"introspection_endpoint"`, `"revocation_endpoint"`} {
		if strings.Contains(body, banned) {
			t.Errorf("discovery doc must not advertise %s; body: %s", banned, body)
		}
	}
}

// containsAll asserts every expected string is present in the slice (order-insensitive).
func containsAll(t *testing.T, field string, got []string, want ...string) {
	t.Helper()
	set := make(map[string]struct{}, len(got))
	for _, g := range got {
		set[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			t.Errorf("%s missing %q (got %v)", field, w, got)
		}
	}
}
