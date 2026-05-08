package oidc

import (
	"fmt"
	"net/url"
	"strings"
)

// validate runs the strict configuration validation pass before any defaults
// are applied. Returns the FIRST violation found. Errors carry stable
// substrings (see spec table) so tests can assert on substrings.
func (i *Issuer) validate() error {
	if i.name == "" {
		return fmt.Errorf("oidc: name must not be empty")
	}
	if i.keyIDExplicit && i.keyID == "" {
		return fmt.Errorf("oidc: key_id must not be empty")
	}
	if i.clientIDExplicit && i.clientID == "" {
		return fmt.Errorf("oidc: client_id must not be empty")
	}
	if i.clientSecretExplicit && i.clientSecret == "" {
		return fmt.Errorf("oidc: client_secret must not be empty")
	}
	if i.defaultSubjectExplicit && i.defaultSubject == "" {
		return fmt.Errorf("oidc: default_subject must not be empty")
	}
	if i.tokenTTLExplicit && i.tokenTTL <= 0 {
		return fmt.Errorf("oidc: token_ttl must be > 0")
	}
	if i.codeTTLExplicit && i.codeTTL <= 0 {
		return fmt.Errorf("oidc: code_ttl must be > 0")
	}
	if err := validateRedirectURIs(i.redirectURIs); err != nil {
		return err
	}
	if err := validateAllowedAudiences(i.allowedAudiences); err != nil {
		return err
	}
	return nil
}

// validateRedirectURIs walks the configured redirect URIs and returns the
// first violation. Per spec: each must be non-empty, parsable, absolute,
// http or https scheme, no fragment, no query, and the list must have no
// duplicates.
func validateRedirectURIs(uris []string) error {
	seen := make(map[string]struct{}, len(uris))
	for _, raw := range uris {
		if raw == "" {
			return fmt.Errorf("oidc: redirect_uri must not be empty")
		}
		if strings.ContainsAny(raw, " \t\r\n") {
			return fmt.Errorf("oidc: redirect_uri %q must not contain whitespace", raw)
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("oidc: redirect_uri %q is not a valid URL: %w", raw, err)
		}
		if u.Scheme == "" {
			return fmt.Errorf("oidc: redirect_uri %q must be absolute http(s)", raw)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("oidc: redirect_uri %q must use http or https scheme", raw)
		}
		if u.Host == "" {
			return fmt.Errorf("oidc: redirect_uri %q must be absolute http(s)", raw)
		}
		if u.Fragment != "" || u.RawFragment != "" {
			return fmt.Errorf("oidc: redirect_uri %q must not contain fragment", raw)
		}
		if u.RawQuery != "" {
			return fmt.Errorf("oidc: redirect_uri %q must not contain query string", raw)
		}
		if _, dup := seen[raw]; dup {
			return fmt.Errorf("oidc: redirect_uri %q is duplicated", raw)
		}
		seen[raw] = struct{}{}
	}
	return nil
}

// validateAllowedAudiences walks the configured audiences and returns the
// first violation: each must be non-empty and the list must have no
// duplicates.
func validateAllowedAudiences(auds []string) error {
	seen := make(map[string]struct{}, len(auds))
	for _, a := range auds {
		if a == "" {
			return fmt.Errorf("oidc: audience must not be empty")
		}
		if strings.ContainsAny(a, " \t\r\n") {
			return fmt.Errorf("oidc: audience %q must not contain whitespace", a)
		}
		if _, dup := seen[a]; dup {
			return fmt.Errorf("oidc: audience %q is duplicated", a)
		}
		seen[a] = struct{}{}
	}
	return nil
}
