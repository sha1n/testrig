package oidc

import (
	"net/http"
	"net/url"
	"slices"
)

// handleAuthorize implements GET /authorize. This task implements only the
// happy paths and the redirect-uri / client_id pre-validation that yields
// HTTP 400 (no redirect). Other error responses come in Task 9.
func (i *Issuer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")

	// Pre-validation: never redirect on bad client_id or redirect_uri.
	if clientID == "" || clientID != i.clientID {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}
	if redirectURI == "" || !slices.Contains(i.redirectURIs, redirectURI) {
		http.Error(w, "invalid_request: redirect_uri", http.StatusBadRequest)
		return
	}
	// Beyond this point, errors come back as 302 to redirectURI.
	if responseType == "" {
		i.redirectError(w, r, redirectURI, q.Get("state"), "invalid_request", "response_type is required")
		return
	}
	if responseType != "code" {
		i.redirectError(w, r, redirectURI, q.Get("state"), "unsupported_response_type", "response_type must be code")
		return
	}

	// Audience validation (if provided).
	audience := q.Get("audience")
	if audience != "" && !slices.Contains(i.allowedAudiences, audience) {
		i.redirectError(w, r, redirectURI, q.Get("state"), "invalid_request", "audience not allowed")
		return
	}

	// Subject extension (testrig-specific).
	subject := q.Get("sub")
	if subject == "" {
		subject = q.Get("subject")
	}
	if subject == "" {
		subject = i.defaultSubject
	}

	rec := &codeRecord{
		clientID:      clientID,
		redirectURI:   redirectURI,
		scope:         q.Get("scope"),
		state:         q.Get("state"),
		nonce:         q.Get("nonce"),
		audience:      audience,
		codeChallenge: q.Get("code_challenge"),
		subject:       subject,
	}
	code, err := i.codeStore.issue(rec)
	if err != nil {
		http.Error(w, "internal_error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	loc := redirectURI + "?code=" + url.QueryEscape(code)
	if rec.state != "" {
		loc += "&state=" + url.QueryEscape(rec.state)
	}
	http.Redirect(w, r, loc, http.StatusFound)
}

// redirectError 302-redirects to redirectURI with OAuth error query params.
// Used for /authorize errors where the redirect target is trusted.
func (i *Issuer) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, errCode, errDesc string) {
	v := url.Values{}
	v.Set("error", errCode)
	v.Set("error_description", errDesc)
	if state != "" {
		v.Set("state", state)
	}
	loc := redirectURI + "?" + v.Encode()
	http.Redirect(w, r, loc, http.StatusFound)
}
