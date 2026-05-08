package oidc

import (
	"net/http"
	"net/url"
	"slices"
	"time"
)

// handleAuthorize implements GET /authorize and POST /authorize. Bad client_id
// or redirect_uri produces HTTP 400 with no redirect (preventing open-redirect
// leaks). All other validation failures 302-redirect to the registered
// redirect_uri with RFC 6749 §5.2 error params.
func (i *Issuer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	var q url.Values
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
			return
		}
		q = r.PostForm
	} else {
		q = r.URL.Query()
	}

	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")

	// Pre-validation: never redirect on bad client_id or redirect_uri.
	// Errors are JSON per RFC 6749 §5.2 (matching /token's error shape).
	if clientID == "" || clientID != i.clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "client_id is missing or invalid")
		return
	}
	if redirectURI == "" || !slices.Contains(i.redirectURIs, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is missing or unregistered")
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
		authTime:      time.Now(),
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
