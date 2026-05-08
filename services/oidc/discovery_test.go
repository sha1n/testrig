package oidc_test

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig/services/oidc"
)

type discoveryDoc struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
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
	if d.UserinfoEndpoint != iss.UserinfoURL() {
		t.Errorf("userinfo_endpoint = %q, want %q", d.UserinfoEndpoint, iss.UserinfoURL())
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
	containsAll(t, "grant_types_supported", d.GrantTypesSupported, "authorization_code", "client_credentials", "refresh_token")
	containsAll(t, "id_token_signing_alg_values_supported", d.IDTokenSigningAlgValuesSupported, "RS256")
	containsAll(t, "subject_types_supported", d.SubjectTypesSupported, "public")
	containsAll(t, "token_endpoint_auth_methods_supported", d.TokenEndpointAuthMethodsSupported, "client_secret_basic", "client_secret_post")
	containsAll(t, "code_challenge_methods_supported", d.CodeChallengeMethodsSupported, "S256")
}

func TestDiscovery_NoFalseAdvertising(t *testing.T) {
	iss := startMinimal(t)
	_, _, body := httpGet(t, iss.DiscoveryURL())
	// We deliberately do not advertise claims_supported,
	// scopes_supported, etc. since the implementation does not enforce them.
	for _, banned := range []string{`"claims_supported"`, `"scopes_supported"`, `"introspection_endpoint"`, `"revocation_endpoint"`} {
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

type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

func TestJWKS_HTTP200_JSONContentType(t *testing.T) {
	iss := startMinimal(t)
	status, headers, _ := httpGet(t, iss.JWKSURL())
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", headers.Get("Content-Type"))
	}
}

func TestJWKS_SingleRSAKey(t *testing.T) {
	iss := startMinimal(t)
	_, _, body := httpGet(t, iss.JWKSURL())
	var d jwksDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Keys) != 1 {
		t.Fatalf("len(keys) = %d, want 1", len(d.Keys))
	}
	k := d.Keys[0]
	if k.Kty != "RSA" {
		t.Errorf("kty = %q, want RSA", k.Kty)
	}
	if k.Use != "sig" {
		t.Errorf("use = %q, want sig", k.Use)
	}
	if k.Alg != "RS256" {
		t.Errorf("alg = %q, want RS256", k.Alg)
	}
}

func TestJWKS_KIDMatchesConfig(t *testing.T) {
	iss := oidc.New("idp").
		WithKeyID("custom-kid-1").
		WithRedirectURIs("http://localhost/cb").
		WithAllowedAudiences("api")
	if _, err := iss.Start(context.Background(), slog.Default()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	_, _, body := httpGet(t, iss.JWKSURL())
	var d jwksDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Keys) == 0 {
		t.Fatalf("no keys in JWKS")
	}
	if d.Keys[0].Kid != "custom-kid-1" {
		t.Errorf("kid = %q, want custom-kid-1", d.Keys[0].Kid)
	}
}

func TestJWKS_KeyResolves_RoundTrip(t *testing.T) {
	iss := startMinimal(t)
	// Sign a token via the issuer.
	tok, err := iss.SignFor("alice", "test-api", time.Minute)
	if err != nil {
		t.Fatalf("SignFor: %v", err)
	}
	// Reconstruct the public key from JWKS.
	_, _, body := httpGet(t, iss.JWKSURL())
	var d jwksDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pub := jwkToRSA(t, d.Keys[0])
	// Verify the token using only the JWKS-derived key.
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) { return pub, nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("token did not validate via JWKS-derived key: %v", err)
	}
}

func jwkToRSA(t *testing.T, k jwk) *rsa.PublicKey {
	t.Helper()
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		t.Fatalf("decode e: %v", err)
	}
	n := new(big.Int).SetBytes(nb)
	e := new(big.Int).SetBytes(eb).Int64()
	return &rsa.PublicKey{N: n, E: int(e)}
}
