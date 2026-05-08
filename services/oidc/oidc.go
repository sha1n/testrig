// Package oidc provides a non-dockerized, OIDC-conformant identity-provider
// fixture for testrig. It implements testrig.Service and exposes:
//
//   - GET /.well-known/jwks.json          — single RSA public key (RS256)
//   - GET /.well-known/openid-configuration — discovery doc
//   - GET /authorize                      — auto-approving auth-code endpoint
//   - POST /token                         — authorization_code + client_credentials
//
// Behavior mirrors Auth0 on the points that matter for testing: per-request
// audience (registered allow-list), id_token aud = client_id, access_token
// aud = requested audience, server-side TTL.
//
// Strictness: misconfiguration is rejected at Start with a descriptive error;
// every endpoint returns RFC 6749 §5.2 errors with the right HTTP status.
//
// PKCE is enforced for the S256 method (RFC 7636). When code_challenge is
// provided at /authorize, code_challenge_method must be S256, and the
// /token request must include a matching code_verifier.
package oidc

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sha1n/testrig"
)

// Issuer is a non-dockerized OIDC-conformant identity-provider fixture.
// Construct with New, configure via the With* methods (chainable), pass to
// env.With(...). Reusable across Start/Stop cycles: Stop releases the
// listener and clears runtime state so a subsequent Start binds a fresh
// port and generates a fresh keypair.
type Issuer struct {
	name string

	// Configured values (mutated by With* setters; snapshotted at Start).
	keyID                  string
	keyIDExplicit          bool
	clientID               string
	clientIDExplicit       bool
	clientSecret           string
	clientSecretExplicit   bool
	redirectURIs           []string
	allowedAudiences       []string
	defaultSubject         string
	defaultSubjectExplicit bool
	tokenTTL               time.Duration
	tokenTTLExplicit       bool
	codeTTL                time.Duration
	codeTTLExplicit        bool

	// Property-key overrides (defaults applied at Start).
	propIssuer       string
	propJWKS         string
	propDiscovery    string
	propClientID     string
	propClientSecret string
	propAudience     string

	// Runtime state (populated during Start, cleared by Stop).
	mu         sync.Mutex
	logger     *slog.Logger
	server     *http.Server
	listener   net.Listener
	baseURL    string
	privKey    *rsa.PrivateKey
	codeStore  *codeStore
	userClaims map[string]map[string]any
}

// New creates an Issuer with default configuration (random KeyID/ClientID/
// ClientSecret/etc applied at Start).
func New(name string) *Issuer {
	return &Issuer{name: name}
}

// WithKeyID overrides the JWT `kid` header value. Empty string is rejected
// at Start.
func (i *Issuer) WithKeyID(kid string) *Issuer { i.keyID = kid; i.keyIDExplicit = true; return i }

// WithClientID overrides the registered client_id. Empty string is rejected
// at Start.
func (i *Issuer) WithClientID(id string) *Issuer {
	i.clientID = id
	i.clientIDExplicit = true
	return i
}

// WithClientSecret overrides the registered client_secret. Empty string is
// rejected at Start.
func (i *Issuer) WithClientSecret(s string) *Issuer {
	i.clientSecret = s
	i.clientSecretExplicit = true
	return i
}

// WithRedirectURIs registers the allowed redirect_uri values. Each must be an
// absolute http(s) URL with no fragment and no query string.
func (i *Issuer) WithRedirectURIs(uris ...string) *Issuer {
	i.redirectURIs = append(i.redirectURIs[:0:0], uris...)
	return i
}

// WithAllowedAudiences registers the audiences the OAuth flow will accept on
// /authorize and /token requests. Empty list disables audience-bound flows
// (auth-code w/o audience and client_credentials).
func (i *Issuer) WithAllowedAudiences(auds ...string) *Issuer {
	i.allowedAudiences = append(i.allowedAudiences[:0:0], auds...)
	return i
}

// WithDefaultSubject sets the default `sub` claim for OAuth-flow tokens.
// Empty string is rejected at Start.
func (i *Issuer) WithDefaultSubject(sub string) *Issuer {
	i.defaultSubject = sub
	i.defaultSubjectExplicit = true
	return i
}

// WithTokenTTL sets the OAuth-flow token lifetime. Must be > 0.
func (i *Issuer) WithTokenTTL(ttl time.Duration) *Issuer {
	i.tokenTTL = ttl
	i.tokenTTLExplicit = true
	return i
}

// WithCodeTTL sets the lifetime of issued authorization codes. Default: 10 minutes.
// Must be > 0.
func (i *Issuer) WithCodeTTL(ttl time.Duration) *Issuer {
	i.codeTTL = ttl
	i.codeTTLExplicit = true
	return i
}

// WithUserClaim adds a custom claim returned by /userinfo for the given
// subject. Multiple calls with the same subject merge keys (last write wins
// per key). Reset by Stop. Useful for tests that need richer profiles.
func (i *Issuer) WithUserClaim(subject, key string, value any) *Issuer {
	if i.userClaims == nil {
		i.userClaims = make(map[string]map[string]any)
	}
	if _, ok := i.userClaims[subject]; !ok {
		i.userClaims[subject] = make(map[string]any)
	}
	i.userClaims[subject][key] = value
	return i
}

// WithIssuerURLPropertyName overrides the published key for the issuer URL
// property. Default: "<name>.issuer".
func (i *Issuer) WithIssuerURLPropertyName(k string) *Issuer { i.propIssuer = k; return i }

// WithJWKSURLPropertyName overrides the published key for the JWKS URL
// property. Default: "<name>.jwks_url".
func (i *Issuer) WithJWKSURLPropertyName(k string) *Issuer { i.propJWKS = k; return i }

// WithDiscoveryURLPropertyName overrides the published key for the discovery
// URL property. Default: "<name>.discovery_url".
func (i *Issuer) WithDiscoveryURLPropertyName(k string) *Issuer { i.propDiscovery = k; return i }

// WithClientIDPropertyName overrides the published key for the client_id
// property. Default: "<name>.client_id".
func (i *Issuer) WithClientIDPropertyName(k string) *Issuer { i.propClientID = k; return i }

// WithClientSecretPropertyName overrides the published key for the
// client_secret property. Default: "<name>.client_secret".
func (i *Issuer) WithClientSecretPropertyName(k string) *Issuer { i.propClientSecret = k; return i }

// WithAudiencePropertyName overrides the published key for the audience
// property (the first allowed audience). Default: "<name>.audience".
func (i *Issuer) WithAudiencePropertyName(k string) *Issuer { i.propAudience = k; return i }

// Name implements testrig.Service.
func (i *Issuer) Name() string { return i.name }

// Start implements testrig.Service.
func (i *Issuer) Start(ctx context.Context, logger *slog.Logger) (testrig.Properties, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.server != nil {
		return nil, fmt.Errorf("oidc: issuer %q already started", i.name)
	}
	if err := i.validate(); err != nil {
		return nil, err
	}

	// Apply defaults for unset fields.
	if !i.keyIDExplicit && i.keyID == "" {
		v, err := generateRandomHex(16)
		if err != nil {
			return nil, err
		}
		i.keyID = v
	}
	if !i.clientIDExplicit && i.clientID == "" {
		v, err := generateRandomHex(32)
		if err != nil {
			return nil, err
		}
		i.clientID = v
	}
	if !i.clientSecretExplicit && i.clientSecret == "" {
		v, err := generateRandomHex(32)
		if err != nil {
			return nil, err
		}
		i.clientSecret = v
	}
	if !i.defaultSubjectExplicit && i.defaultSubject == "" {
		i.defaultSubject = "test-user"
	}
	if !i.tokenTTLExplicit && i.tokenTTL == 0 {
		i.tokenTTL = time.Hour
	}
	if !i.codeTTLExplicit && i.codeTTL == 0 {
		i.codeTTL = 10 * time.Minute
	}

	i.logger = logger
	i.logger.Info("🎬 Starting OIDC issuer service", "name", i.name)

	if err := i.startServer(ctx); err != nil {
		return nil, err
	}

	props := i.buildProperties()
	return props, nil
}

// buildProperties returns the testrig.Properties published by Start.
// Default keys "<name>.<suffix>" unless overridden via With*PropertyName.
func (i *Issuer) buildProperties() testrig.Properties {
	keyOr := func(override, suffix string) string {
		if override != "" {
			return override
		}
		return i.name + "." + suffix
	}
	audValue := ""
	if len(i.allowedAudiences) > 0 {
		audValue = i.allowedAudiences[0]
	}
	return testrig.Properties{
		keyOr(i.propIssuer, "issuer"):              i.IssuerURL(),
		keyOr(i.propJWKS, "jwks_url"):              i.JWKSURL(),
		keyOr(i.propDiscovery, "discovery_url"):    i.DiscoveryURL(),
		keyOr(i.propClientID, "client_id"):         i.clientID,
		keyOr(i.propClientSecret, "client_secret"): i.clientSecret,
		keyOr(i.propAudience, "audience"):          audValue,
	}
}

// Stop implements testrig.Service. Idempotent.
func (i *Issuer) Stop(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.server == nil {
		return nil
	}
	if i.logger != nil {
		i.logger.Info("🛑 Stopping OIDC issuer service", "name", i.name)
	}
	return i.stopServer(ctx)
}

// Typed accessors. URL accessors return "" before Start.

// IssuerURL returns the issuer URL, e.g. "http://127.0.0.1:34567". Empty before Start.
func (i *Issuer) IssuerURL() string { return i.baseURL }

// JWKSURL returns the JWKS endpoint URL. Empty before Start.
func (i *Issuer) JWKSURL() string {
	if i.baseURL == "" {
		return ""
	}
	return i.baseURL + "/.well-known/jwks.json"
}

// AuthorizationURL returns the /authorize endpoint URL. Empty before Start.
func (i *Issuer) AuthorizationURL() string {
	if i.baseURL == "" {
		return ""
	}
	return i.baseURL + "/authorize"
}

// TokenURL returns the /token endpoint URL. Empty before Start.
func (i *Issuer) TokenURL() string {
	if i.baseURL == "" {
		return ""
	}
	return i.baseURL + "/token"
}

// DiscoveryURL returns the OIDC discovery URL. Empty before Start.
func (i *Issuer) DiscoveryURL() string {
	if i.baseURL == "" {
		return ""
	}
	return i.baseURL + "/.well-known/openid-configuration"
}

// UserinfoURL returns the /userinfo endpoint URL. Empty before Start.
func (i *Issuer) UserinfoURL() string {
	if i.baseURL == "" {
		return ""
	}
	return i.baseURL + "/userinfo"
}

// ClientID returns the registered client_id. Empty until configured or Start.
func (i *Issuer) ClientID() string { return i.clientID }

// ClientSecret returns the registered client_secret. Empty until configured or Start.
func (i *Issuer) ClientSecret() string { return i.clientSecret }

// AllowedAudiences returns a copy of the configured allow-list.
func (i *Issuer) AllowedAudiences() []string {
	out := make([]string, len(i.allowedAudiences))
	copy(out, i.allowedAudiences)
	return out
}

// PublicKey returns the RSA public key used to sign tokens. nil before Start.
func (i *Issuer) PublicKey() *rsa.PublicKey {
	if i.privKey == nil {
		return nil
	}
	return &i.privKey.PublicKey
}

// Sign signs the provided claims with the issuer's RSA key. Auto-fills `iss`
// and `iat` when absent in claims; never overrides caller-supplied values
// (so negative-test scenarios can mint tokens with wrong iss). The `kid`
// header is always set. Returns an error if called before Start.
func (i *Issuer) Sign(claims jwt.MapClaims) (string, error) {
	return i.sign(claims)
}

// SignFor is a convenience wrapper that signs a token with a specific
// subject, audience, and TTL. Validates inputs strictly: empty subject,
// empty audience, or non-positive ttl all return an error. Note that the
// audience here is NOT validated against AllowedAudiences — this is the
// test-side mint helper, intentionally permissive for negative test cases.
func (i *Issuer) SignFor(subject, audience string, ttl time.Duration) (string, error) {
	return i.signFor(subject, audience, ttl)
}
