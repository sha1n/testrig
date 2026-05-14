# oidc — testrig service

An in-process, Auth0-style OIDC identity-provider fixture — no Docker, no network round-trips to an external IdP.

The `oidc.Issuer` is a fully-functional OIDC issuer that binds to a random port on `127.0.0.1`, generates a fresh RSA-2048 keypair, and serves the five endpoint families required by OIDC Core and RFC 6749. It supports `authorization_code` (with optional PKCE S256), `client_credentials`, and `refresh_token` (with rotation) grant types. In a `testrig.Env`, `Issuer` implements `testrig.Service`: `Start` brings the HTTP server up and publishes seven properties — URLs, client credentials, and audience — which your test can wire directly into the application's config. `Stop` is idempotent and clears all runtime state, so the same `Issuer` instance can be restarted between test suites with a fresh keypair and a fresh port.

## Install

This is a separate Go module. Pin to the current prototype tag while the API iterates:

```
go get github.com/sha1n/testrig/services/oidc@v0.0.0-prototype.1
```

It transitively pulls in `github.com/sha1n/testrig` and `github.com/golang-jwt/jwt/v5`. No Docker required. See the top-level README for guidance on `@latest` vs. explicit pinning.

## Quickstart

The most common scenario: your service validates incoming JWTs via JWKS. Use `SignFor` to mint a token on the test side and `JWKSURL` to point the validator at the fixture.

```go
package myservice_test

import (
    "context"
    "log/slog"
    "net/http"
    "testing"
    "time"

    "github.com/sha1n/testrig"
    "github.com/sha1n/testrig/services/oidc"
)

func TestValidator(t *testing.T) {
    iss := oidc.New("idp").
        WithAllowedAudiences("my-api").
        WithRedirectURIs("http://localhost:8080/callback")

    if _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
        t.Fatalf("start issuer: %v", err)
    }
    t.Cleanup(func() { _ = iss.Stop(context.Background()) })

    // Mint a token for subject "alice" targeting "my-api", valid for 1 minute.
    token, err := iss.SignFor("alice", "my-api", time.Minute)
    if err != nil {
        t.Fatalf("sign token: %v", err)
    }

    // Build a request with a Bearer header.
    req, _ := http.NewRequest(http.MethodGet, "http://app-under-test/protected", nil)
    req.Header.Set("Authorization", "Bearer "+token)

    // Tell the application to resolve its JWKS from the fixture.
    // How this is wired depends on your app's config; the issuer URL and JWKS
    // URL are available as typed accessors and also in env.Properties().
    _ = iss.IssuerURL() // e.g. "http://127.0.0.1:54321"
    _ = iss.JWKSURL()   // e.g. "http://127.0.0.1:54321/.well-known/jwks.json"

    // ... execute the request against your validator, assert 200.
}
```

## Endpoints

| Path | Methods | Description |
|---|---|---|
| `/.well-known/jwks.json` | GET | Single RSA-2048 public key in JWK Set format; `kid` matches the configured or generated `KeyID`. |
| `/.well-known/openid-configuration` | GET | OIDC discovery document; all endpoint URLs are derived from the bound address. |
| `/authorize` | GET, POST | Auto-approving authorization endpoint; issues a one-time code on every valid request (no login UI, no consent screen). |
| `/token` | POST | Token endpoint; dispatches on `grant_type` — `authorization_code`, `client_credentials`, `refresh_token`. Enforces RFC 6749 §2.3.1 client authentication (Basic or body, not both). |
| `/userinfo` | GET, POST | OIDC Core §5.3 userinfo endpoint; validates the Bearer token and returns `sub` plus any claims registered with `WithUserClaim`. |

## Supported flows

### authorization_code

`/authorize` validates `client_id`, `redirect_uri`, `response_type=code`, and (if provided) the `audience` parameter against the allow-list. It then issues a 32-byte random code and 302-redirects to `redirect_uri?code=...&state=...`. The code is single-use and expires after `CodeTTL` (default 10 minutes). `/token` exchanges the code for an `id_token` and, when an `audience` was requested, an `access_token`. The `id_token` audience is always `client_id` (OIDC Core §2); the `access_token` audience is the requested audience.

PKCE S256 is enforced end-to-end: if `code_challenge` is present at `/authorize`, `code_challenge_method` must be `S256`; the `/token` request must then supply a matching `code_verifier` (43–128 chars, unreserved alphabet, RFC 7636 §4). The `plain` method is rejected.

A testrig-specific extension: the `/authorize` request may include `sub` (or `subject`) to override the default subject for the issued tokens. This lets a single issuer serve tests with different user identities without restarting.

### client_credentials

POST to `/token` with `grant_type=client_credentials`, valid client credentials, and an `audience` that is in the `AllowedAudiences` list. Returns an `access_token` with `sub=<client_id>` (Auth0 convention), `aud=<audience>`, and a `jti` claim (RFC 9068 §2.2). No `id_token` is issued. Scope is passed through to the token and response body if present.

### refresh_token

A refresh token is issued in the `authorization_code` response only when `offline_access` is in the `scope` parameter. Refresh tokens are JWT-formatted, signed by the same key, and stored server-side keyed by `jti`. Each successful refresh exchange **rotates** the token: the old token is invalidated and a new one is returned alongside a fresh `access_token`. Replaying an old token returns `invalid_grant`. The token store is cleared on `Stop`.

## Configuration

All setters are chainable and must be called before `Start`. Explicit values are validated at `Start`; defaults are applied for unset fields.

### Identity

| Setter | Default | Controls |
|---|---|---|
| `WithKeyID(kid string)` | random 16-byte hex | JWT `kid` header in all signed tokens; must be non-empty if set explicitly. |
| `WithClientID(id string)` | random 32-byte hex | Registered `client_id`; validated on every `/authorize` and `/token` request. |
| `WithClientSecret(s string)` | random 32-byte hex | Registered `client_secret`; validated on every `/token` request. |

### Policy

| Setter | Default | Controls |
|---|---|---|
| `WithRedirectURIs(uris ...string)` | none | Allow-list for `redirect_uri`. Each must be an absolute `http`/`https` URL with no fragment and no query string. Validation rejects duplicates and whitespace. |
| `WithAllowedAudiences(auds ...string)` | none | Allow-list for `audience` on `/authorize` and `/token`. An empty list disables audience-bound flows. Validation rejects duplicates and whitespace. |
| `WithDefaultSubject(sub string)` | `"test-user"` | `sub` claim used when `/authorize` is called without an explicit `sub`/`subject` parameter. |

### TTLs

| Setter | Default | Controls |
|---|---|---|
| `WithTokenTTL(ttl time.Duration)` | `1h` | Lifetime of issued access tokens and id tokens. Must be > 0. |
| `WithCodeTTL(ttl time.Duration)` | `10m` | Lifetime of authorization codes issued by `/authorize`. Must be > 0. |
| `WithRefreshTokenTTL(ttl time.Duration)` | `720h` (30 days) | Lifetime of refresh tokens. Must be > 0. |

### User profile

| Setter | Controls |
|---|---|
| `WithUserClaim(subject, key string, value any)` | Registers a custom claim returned by `/userinfo` for the given subject. Multiple calls on the same subject merge keys (last write wins per key). Cleared on `Stop`. |

### Published property key overrides

By default the issuer publishes properties under `<name>.<suffix>` keys. Use these setters when your application reads config from a specific key name.

| Setter | Default key |
|---|---|
| `WithIssuerURLPropertyName(k string)` | `<name>.issuer` |
| `WithJWKSURLPropertyName(k string)` | `<name>.jwks_url` |
| `WithDiscoveryURLPropertyName(k string)` | `<name>.discovery_url` |
| `WithUserinfoURLPropertyName(k string)` | `<name>.userinfo_url` |
| `WithClientIDPropertyName(k string)` | `<name>.client_id` |
| `WithClientSecretPropertyName(k string)` | `<name>.client_secret` |
| `WithAudiencePropertyName(k string)` | `<name>.audience` |

## Published properties

`Start` returns a `testrig.Properties` map (also available via `env.Properties()`) containing the following seven keys. All URL values are empty strings if `Start` has not been called.

| Key (default) | Value |
|---|---|
| `<name>.issuer` | Issuer URL, e.g. `http://127.0.0.1:54321`. Suitable as the `iss` claim validator and OIDC `issuer` config field. |
| `<name>.jwks_url` | JWKS endpoint URL, e.g. `http://127.0.0.1:54321/.well-known/jwks.json`. |
| `<name>.discovery_url` | OIDC discovery URL, e.g. `http://127.0.0.1:54321/.well-known/openid-configuration`. |
| `<name>.userinfo_url` | Userinfo endpoint URL. |
| `<name>.client_id` | Registered `client_id` (auto-generated if not set explicitly). |
| `<name>.client_secret` | Registered `client_secret` (auto-generated if not set explicitly). |
| `<name>.audience` | First value in `AllowedAudiences`, or empty string if none configured. |

## Typed accessors

These methods return live values and are safe to call after `Start`. URL accessors return an empty string before `Start`.

| Accessor | Returns |
|---|---|
| `IssuerURL() string` | Base URL of the issuer, e.g. `http://127.0.0.1:54321`. |
| `JWKSURL() string` | `IssuerURL + "/.well-known/jwks.json"` |
| `AuthorizationURL() string` | `IssuerURL + "/authorize"` |
| `TokenURL() string` | `IssuerURL + "/token"` |
| `DiscoveryURL() string` | `IssuerURL + "/.well-known/openid-configuration"` |
| `UserinfoURL() string` | `IssuerURL + "/userinfo"` |
| `ClientID() string` | Registered client ID. Non-empty after explicit `WithClientID` or after `Start`. |
| `ClientSecret() string` | Registered client secret. Non-empty after explicit `WithClientSecret` or after `Start`. |
| `AllowedAudiences() []string` | Copy of the configured audience allow-list. |
| `PublicKey() *rsa.PublicKey` | RSA public key used to sign tokens. `nil` before `Start`. |

## Token shapes

All tokens are RS256-signed JWTs. The `kid` header is always set to the configured `KeyID`.

### id_token (authorization_code flow)

Returned as `id_token` in the `/token` response.

| Claim | Value | Reference |
|---|---|---|
| `iss` | Issuer URL | OIDC Core §2 |
| `sub` | Subject (default or per-request override) | OIDC Core §2 |
| `aud` | `client_id` (always — id token audience is the RP) | OIDC Core §2 |
| `iat` | Unix timestamp of issuance | RFC 7519 §4.1.6 |
| `exp` | `iat + TokenTTL` | RFC 7519 §4.1.4 |
| `auth_time` | Unix timestamp of the `/authorize` request | OIDC Core §2 |
| `nonce` | Echoed from `/authorize` if present | OIDC Core §3.1.2 |
| `azp` | `client_id`, only when `audience` was requested | OIDC Core §2 |

### access_token (authorization_code and client_credentials flows)

Returned as `access_token`; only present when an `audience` was requested.

| Claim | Value | Reference |
|---|---|---|
| `iss` | Issuer URL | RFC 9068 §2.2 |
| `sub` | Subject (or `client_id` for client_credentials) | RFC 9068 §2.2 |
| `aud` | Requested audience | RFC 9068 §2.2 |
| `iat`, `exp` | Issuance and expiry | RFC 7519 |
| `jti` | Random 16-byte hex string | RFC 9068 §2.2 |
| `scope` | Echoed from request if present | RFC 6749 §4.1.2 |

### refresh_token

A signed JWT stored server-side by `jti`. Not intended for direct inspection in tests, but the shape is:

| Claim | Value |
|---|---|
| `iss` | Issuer URL |
| `sub` | Subject |
| `aud` | `<issuerURL>/refresh` (distinguishes refresh from access tokens) |
| `client_id` | Registered client ID |
| `scope` | Scope from the original grant |
| `jti` | Random 16-byte hex string (store key) |
| `iat`, `exp` | Issuance and expiry (`iat + RefreshTokenTTL`) |

## Test-side mint helpers

### `Sign(claims jwt.MapClaims) (string, error)`

Signs arbitrary claims with the issuer's RSA key. Auto-fills `iss` (issuer URL) and `iat` (current Unix time) only when those keys are absent in the provided map — it never overrides caller-supplied values. The `kid` header is always set. Returns an error if called before `Start`.

Use `Sign` when you need full control over the token contents: expired tokens, wrong-issuer tokens, missing-claim tests, or any negative test that requires the token to be structurally valid but semantically wrong.

```go
// Token with an expired exp — valid signature, will fail expiry check.
tok, _ := iss.Sign(jwt.MapClaims{
    "sub": "alice",
    "aud": "my-api",
    "exp": time.Now().Add(-time.Hour).Unix(),
})
```

### `SignFor(subject, audience string, ttl time.Duration) (string, error)`

Convenience wrapper: sets `iss`, `sub`, `aud`, `iat`, and `exp` from the arguments. Validates that `subject` and `audience` are non-empty and `ttl > 0`. The `audience` is **not** checked against `AllowedAudiences` — this is intentional so that negative tests can mint tokens for disallowed audiences.

```go
tok, err := iss.SignFor("alice", "my-api", time.Minute)
```

Both helpers bypass the OAuth flows entirely and are the right choice when you are testing a JWT validator, not an OAuth client. For testing the OAuth flows themselves, drive `/authorize` and `/token` via `http.Client`.

## Lifecycle

**`Start(ctx, logger)`** validates configuration (returns a descriptive error on the first violation), generates a fresh RSA-2048 keypair, binds a TCP listener on `127.0.0.1:0` (random port), registers all routes, and starts an HTTP server in a background goroutine. It publishes seven properties and returns them. Calling `Start` on an already-started issuer returns an error containing `"already started"`.

**`Stop(ctx)`** calls `http.Server.Shutdown(ctx)`, clears all runtime state (base URL, keypair, code store, refresh store, user claims), and is idempotent — calling it before `Start` or multiple times is safe and returns `nil`.

After `Stop`, the `Issuer` struct can be reconfigured and restarted. The new `Start` generates a different keypair and binds a different port. This is useful for key-rollover tests.

Configuration is validated strictly: if you pass an explicit value via a `With*` setter, it is validated at `Start`. Random defaults (KeyID, ClientID, ClientSecret) are generated only when no explicit value was set.

## Common patterns

### 1. Testing a JWT validator

Start the issuer, mint a token via `SignFor`, call your validator, confirm it accepts the token. Point the validator at `JWKSURL` or `IssuerURL`.

```go
func TestMyHandler_AcceptsValidToken(t *testing.T) {
    iss := oidc.New("idp").
        WithAllowedAudiences("my-api").
        WithRedirectURIs("http://localhost/cb")
    _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = iss.Stop(context.Background()) })

    // Wire the issuer URL and JWKS URL into the service under test.
    svc := myservice.New(myservice.Config{
        IssuerURL: iss.IssuerURL(),
        JWKSURL:   iss.JWKSURL(),
    })

    tok, _ := iss.SignFor("alice", "my-api", time.Minute)
    req := httptest.NewRequest(http.MethodGet, "/protected", nil)
    req.Header.Set("Authorization", "Bearer "+tok)
    w := httptest.NewRecorder()
    svc.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Errorf("status = %d, want 200", w.Code)
    }
}
```

### 2. Testing an OIDC RP doing the authorization_code flow

Drive `/authorize` directly with an `http.Client` that does not follow redirects, extract the code from the `Location` header, then POST to `/token`.

```go
func TestAuthCodeFlow(t *testing.T) {
    iss := oidc.New("idp").
        WithAllowedAudiences("my-api").
        WithRedirectURIs("http://localhost:8080/callback").
        WithDefaultSubject("alice")
    _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = iss.Stop(context.Background()) })

    client := &http.Client{
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        },
    }

    // Step 1: /authorize
    q := url.Values{
        "client_id":     {iss.ClientID()},
        "redirect_uri":  {"http://localhost:8080/callback"},
        "response_type": {"code"},
        "audience":      {"my-api"},
        "state":         {"random-state"},
    }
    resp, _ := client.Get(iss.AuthorizationURL() + "?" + q.Encode())
    loc, _ := url.Parse(resp.Header.Get("Location"))
    code := loc.Query().Get("code")

    // Step 2: /token
    form := url.Values{
        "grant_type":   {"authorization_code"},
        "code":         {code},
        "redirect_uri": {"http://localhost:8080/callback"},
    }
    req, _ := http.NewRequest(http.MethodPost, iss.TokenURL(), strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth(iss.ClientID(), iss.ClientSecret())
    tokenResp, _ := client.Do(req)

    var body struct {
        IDToken     string `json:"id_token"`
        AccessToken string `json:"access_token"`
    }
    _ = json.NewDecoder(tokenResp.Body).Decode(&body)
    // body.IDToken and body.AccessToken are ready for inspection.
}
```

### 3. Testing a service-to-service client_credentials flow

```go
func TestClientCredentials(t *testing.T) {
    iss := oidc.New("idp").
        WithClientID("svc-a").
        WithClientSecret("s3cr3t").
        WithAllowedAudiences("svc-b").
        WithRedirectURIs("http://localhost/cb") // required by validation
    _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = iss.Stop(context.Background()) })

    form := url.Values{
        "grant_type": {"client_credentials"},
        "audience":   {"svc-b"},
    }
    req, _ := http.NewRequest(http.MethodPost, iss.TokenURL(), strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth("svc-a", "s3cr3t")
    resp, _ := http.DefaultClient.Do(req)

    var tok struct {
        AccessToken string `json:"access_token"`
    }
    _ = json.NewDecoder(resp.Body).Decode(&tok)
    // tok.AccessToken has sub=svc-a, aud=svc-b, signed by the fixture key.
}
```

### 4. Testing refresh token rotation

```go
func TestRefreshRotation(t *testing.T) {
    iss := oidc.New("idp").
        WithAllowedAudiences("my-api").
        WithRedirectURIs("http://localhost:8080/callback")
    _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = iss.Stop(context.Background()) })

    // Get initial tokens with offline_access scope.
    // (run the auth_code flow as in pattern 2, with scope=offline_access)
    // first.RefreshToken is the initial refresh token.

    form := url.Values{
        "grant_type":    {"refresh_token"},
        "refresh_token": {first.RefreshToken},
    }
    req, _ := http.NewRequest(http.MethodPost, iss.TokenURL(), strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth(iss.ClientID(), iss.ClientSecret())
    resp, _ := http.DefaultClient.Do(req)

    var second struct {
        AccessToken  string `json:"access_token"`
        RefreshToken string `json:"refresh_token"`
    }
    _ = json.NewDecoder(resp.Body).Decode(&second)
    // second.RefreshToken != first.RefreshToken (rotated).
    // Replaying first.RefreshToken now returns invalid_grant (400).
}
```

## Gaps and workarounds

These are intentional or structural divergences from real Auth0. The service covers the overwhelming majority of Go HTTP service testing; these gaps matter only for the specific scenarios noted.

### No `id_token` in refresh response

**Affects:** Server-rendered web apps with sessions that rely on a fresh `id_token` on each refresh to update user identity (Next.js `nextjs-auth0`, Spring Security session refresh, Go SSR apps with session cookies).

**In practice:** Go HTTP services that validate `access_token` via JWKS on every request — which is the standard pattern — are not affected.

**Workaround:** Extend the service by embedding `Issuer` and overriding `handleRefreshGrant`, or use a real IdP for that specific test.

### Refresh grant ignores `scope` narrowing

RFC 6749 §6 allows the client to request a narrower scope on refresh; real Auth0 honors it.

**Affects:** Clients that explicitly narrow scope on refresh.

**In practice:** The Go standard `oauth2` package and most real consumers do not expose scope narrowing on refresh. A very narrow scenario.

### Single-client model

One `client_id`/`client_secret` pair per `Issuer` instance.

**Affects:** Tests that require multiple OAuth clients interacting with a single issuer.

**Workaround:** Instantiate multiple `Issuer` instances with distinct `WithClientID`. Each acts as a separate "tenant" or "client". They can coexist in the same `testrig.Env`.

### No `/revocation` endpoint (RFC 7009)

Apps that explicitly POST to `/oauth/revoke` on logout will get a 404.

**In practice:** Auth0's primary logout pattern is `/v2/logout` (browser redirect), which this fixture also does not model. Most Go services test local cookie/session cleanup rather than server-side token revocation.

**Workaround:** Skip revocation tests or use a real IdP. For logout scenarios, test the local session-clearing logic directly without going through the issuer.

### No `/introspection` endpoint (RFC 7662)

Apps that POST to `/oauth/introspect` for token validation will get a 404.

**In practice:** Introspection is relevant only for opaque access tokens. This fixture mints JWT access tokens; apps validate them locally via JWKS. A non-issue for typical Go services.

**Workaround:** If your app must call introspect, intercept the call in a `httptest.Server` stub.

### Plain HTTP only, no TLS

The server binds to `127.0.0.1:<random>` over plain HTTP.

**Affects:** Clients that hard-require `https://` in issuer or JWKS URLs (some strict OIDC validators check the scheme).

**In practice:** `coreos/go-oidc`, `golang.org/x/oauth2`, and other major Go OIDC clients accept `http://` URLs for testing.

**Workaround:** Wrap the issuer's HTTP handler with `httptest.NewTLSServer` if your validator enforces HTTPS.

### `offline_access` does not require `openid` scope

OIDC Core §11 says `offline_access` should only be granted in conjunction with `openid` scope; real Auth0 enforces this.

**Affects:** Tests of the negative case where the IdP rejects `offline_access` without `openid`.

**In practice:** A very narrow scenario. If your application only requests `offline_access`, you will get a refresh token even without `openid`.

### PKCE S256 only

RFC 7636 defines a `plain` method (deprecated and insecure). This fixture rejects `plain` at `/authorize` with `invalid_request`.

**Affects:** Nothing modern. `plain` PKCE was deprecated for new clients at the time RFC 7636 was written.

### RS256 only

All tokens are signed with RS256. No HS256, ES256, or EdDSA.

**Affects:** Validators that hardcode an expected algorithm other than RS256.

**In practice:** RS256 is the dominant algorithm in OIDC deployments and the default for Auth0, Okta, and most providers.

**Workaround:** If your validator expects a different algorithm, use a real IdP or build a custom fixture.

### No multi-key JWKS / key rotation

The JWKS endpoint always returns exactly one key: the key generated at `Start`.

**Affects:** Tests of key-rollover scenarios (e.g. validator should accept tokens signed by either the old or new key during a rotation window).

**Workaround:** `Stop` the issuer and `Start` it again — a new keypair and new port are generated. The previous `IssuerURL` becomes invalid, modelling "old key revoked". For the dual-key window scenario, two simultaneous `Issuer` instances are the closest approximation.

### `/authorize` auto-approves, no consent denial path

Every valid `/authorize` request immediately redirects with a code. There is no way to test the "user denied consent" path through the normal flow.

**Affects:** Tests of RP error handling when the IdP returns `access_denied`.

**In practice:** Most application tests do not model the user-denial path.

**Workaround:** Return a `302` to `redirect_uri?error=access_denied` directly from a small `httptest.Server` stub rather than using this fixture for that specific case.
