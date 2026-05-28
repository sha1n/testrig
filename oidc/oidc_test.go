package oidc_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/oidc"
)

func TestStart_MinimalConfig_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/cb").
		WithAllowedAudiences("api")
	props, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
	if iss.IssuerURL() == "" {
		t.Errorf("IssuerURL is empty after Start")
	}
	if props == nil {
		t.Errorf("Start returned nil properties")
	}
}

func TestStart_FullConfig_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithKeyID("kid-1").
		WithClientID("client-abc").
		WithClientSecret("secret-xyz").
		WithRedirectURIs("http://localhost:8080/cb", "http://localhost:9090/cb").
		WithAllowedAudiences("api-1", "api-2").
		WithDefaultSubject("alice").
		WithTokenTTL(2 * time.Minute).
		WithIssuerURLPropertyName("ISSUER").
		WithJWKSURLPropertyName("JWKS").
		WithDiscoveryURLPropertyName("DISCOVERY").
		WithUserinfoURLPropertyName("USERINFO").
		WithClientIDPropertyName("CLIENT_ID").
		WithClientSecretPropertyName("CLIENT_SECRET").
		WithAudiencePropertyName("AUDIENCE")
	if _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
}

func TestStart_StartTwice_ReturnsError(t *testing.T) {
	iss := startMinimal(t)
	_, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err == nil {
		t.Fatal("expected error on second Start, got nil")
	}
	if !strings.Contains(err.Error(), "already started") {
		t.Errorf("expected error to mention 'already started', got %v", err)
	}
}

func TestStop_StopThenStart_Succeeds(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/cb").
		WithAllowedAudiences("api")
	if _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := iss.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer func() { _ = iss.Stop(context.Background()) }()
	if iss.IssuerURL() == "" {
		t.Error("IssuerURL empty after restart")
	}
}

func TestStop_BeforeStart_NoOp(t *testing.T) {
	iss := oidc.New("idp")
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("Stop before Start should be no-op, got %v", err)
	}
}

func TestStop_Twice_NoOp(t *testing.T) {
	iss := startMinimal(t)
	// Stop is called twice here; t.Cleanup from startMinimal will call it
	// a third time. All three calls should succeed without error.
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("first explicit Stop: %v", err)
	}
	if err := iss.Stop(context.Background()); err != nil {
		t.Errorf("second Stop should be no-op, got %v", err)
	}
}

// validationCase exercises one validation rule: it constructs an Issuer,
// applies an offending With* setter, calls Start, and asserts the returned
// error contains a stable substring.
type validationCase struct {
	apply   func(*oidc.Issuer)
	wantSub string
}

func runValidationCase(t *testing.T, tc validationCase) {
	t.Helper()
	iss := oidc.New("idp")
	tc.apply(iss)
	_, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", tc.wantSub)
	}
	if !strings.Contains(err.Error(), tc.wantSub) {
		t.Errorf("error %q does not contain expected substring %q", err.Error(), tc.wantSub)
	}
	// Don't Stop — Start failed, nothing to clean up.
}

func TestStart_EmptyName_ReturnsError(t *testing.T) {
	iss := oidc.New("")
	_, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err == nil || !strings.Contains(err.Error(), "name must not be empty") {
		t.Errorf("expected name-empty error, got %v", err)
	}
}

func TestStart_EmptyKeyID_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithKeyID("") },
		wantSub: "key_id must not be empty",
	})
}

func TestStart_EmptyClientID_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithClientID("") },
		wantSub: "client_id must not be empty",
	})
}

func TestStart_EmptyClientSecret_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithClientSecret("") },
		wantSub: "client_secret must not be empty",
	})
}

func TestStart_EmptyDefaultSubject_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithDefaultSubject("") },
		wantSub: "default_subject must not be empty",
	})
}

func TestStart_ZeroTokenTTL_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithTokenTTL(0) },
		wantSub: "token_ttl must be > 0",
	})
}

func TestStart_NegativeTokenTTL_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithTokenTTL(-1 * time.Second) },
		wantSub: "token_ttl must be > 0",
	})
}

func TestStart_RedirectURI_EmptyEntry(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("") },
		wantSub: "redirect_uri must not be empty",
	})
}

func TestStart_RedirectURI_Unparsable(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("ht!tp://[::1]:bad") },
		wantSub: "is not a valid URL",
	})
}

func TestStart_RedirectURI_Relative(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("/callback") },
		wantSub: "must be absolute",
	})
}

func TestStart_RedirectURI_NonHTTPScheme(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("javascript:alert(1)") },
		wantSub: "must use http or https scheme",
	})
}

func TestStart_RedirectURI_HasFragment(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("http://localhost/cb#frag") },
		wantSub: "must not contain fragment",
	})
}

func TestStart_RedirectURI_HasQuery(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("http://localhost/cb?x=1") },
		wantSub: "must not contain query string",
	})
}

func TestStart_RedirectURI_Duplicate(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("http://localhost/cb", "http://localhost/cb") },
		wantSub: "is duplicated",
	})
}

func TestStart_RedirectURI_Whitespace(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRedirectURIs("http://localhost/cb ") },
		wantSub: "must not contain whitespace",
	})
}

func TestStart_AudienceEmptyEntry_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithAllowedAudiences("api", "") },
		wantSub: "audience must not be empty",
	})
}

func TestStart_AudienceDuplicate_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithAllowedAudiences("api", "api") },
		wantSub: "is duplicated",
	})
}

func TestStart_AudienceWhitespace_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithAllowedAudiences("api", "bad api") },
		wantSub: "must not contain whitespace",
	})
}

func TestStart_ZeroCodeTTL_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithCodeTTL(0) },
		wantSub: "code_ttl must be > 0",
	})
}

func TestStart_ZeroRefreshTokenTTL_ReturnsError(t *testing.T) {
	runValidationCase(t, validationCase{
		apply:   func(i *oidc.Issuer) { i.WithRefreshTokenTTL(0) },
		wantSub: "refresh_token_ttl must be > 0",
	})
}

// Property tests do not use startMinimal because they need access to the
// Properties map returned by Start (startMinimal discards it). They start
// the Issuer directly and clean up via t.Cleanup.

func TestProperties_DefaultKeys_AllPresent(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost/cb").
		WithAllowedAudiences("test-api")
	props, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	expected := map[string]string{
		"idp.issuer":        iss.IssuerURL(),
		"idp.jwks_url":      iss.JWKSURL(),
		"idp.discovery_url": iss.DiscoveryURL(),
		"idp.userinfo_url":  iss.UserinfoURL(),
		"idp.client_id":     iss.ClientID(),
		"idp.client_secret": iss.ClientSecret(),
		"idp.audience":      iss.AllowedAudiences()[0],
	}
	for k, want := range expected {
		got, ok := props[k]
		if !ok {
			t.Errorf("property %q missing", k)
			continue
		}
		if got != want {
			t.Errorf("property %q = %q, want %q", k, got, want)
		}
	}
}

func TestProperties_AudienceProperty_EmptyWhenNoAllowedAudiences(t *testing.T) {
	iss := oidc.New("idp").WithRedirectURIs("http://localhost/cb")
	props, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	if props["idp.audience"] != "" {
		t.Errorf("idp.audience = %q, want empty", props["idp.audience"])
	}
}

func TestProperties_OverrideKeys_TakeEffect(t *testing.T) {
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost/cb").
		WithAllowedAudiences("test-api").
		WithIssuerURLPropertyName("ISSUER").
		WithJWKSURLPropertyName("JWKS").
		WithDiscoveryURLPropertyName("DISCOVERY").
		WithUserinfoURLPropertyName("USERINFO").
		WithClientIDPropertyName("CLIENT_ID").
		WithClientSecretPropertyName("CLIENT_SECRET").
		WithAudiencePropertyName("AUDIENCE")
	props, err := iss.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	expected := map[string]string{
		"ISSUER":        iss.IssuerURL(),
		"JWKS":          iss.JWKSURL(),
		"DISCOVERY":     iss.DiscoveryURL(),
		"USERINFO":      iss.UserinfoURL(),
		"CLIENT_ID":     iss.ClientID(),
		"CLIENT_SECRET": iss.ClientSecret(),
		"AUDIENCE":      iss.AllowedAudiences()[0],
	}
	for k, want := range expected {
		got, ok := props[k]
		if !ok {
			t.Errorf("override key %q missing", k)
			continue
		}
		if got != want {
			t.Errorf("override key %q = %q, want %q", k, got, want)
		}
	}
	// Confirm default-named keys are NOT present.
	for _, k := range []string{"idp.issuer", "idp.jwks_url", "idp.discovery_url", "idp.userinfo_url", "idp.client_id", "idp.client_secret", "idp.audience"} {
		if _, ok := props[k]; ok {
			t.Errorf("default key %q should be absent when override is set", k)
		}
	}
}
