package oidc_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sha1n/testrig/api"
	"github.com/sha1n/testrig/services/oidc"
)

// startMinimal starts an Issuer with the smallest set of options that pass
// validation: WithRedirectURIs and WithAllowedAudiences populated so all
// flows can be exercised. The test cleans up via t.Cleanup.
func startMinimal(t *testing.T) *oidc.Issuer {
	t.Helper()
	iss := oidc.New("idp").
		WithRedirectURIs("http://localhost:8080/callback").
		WithAllowedAudiences("test-api")
	if _, err := iss.Start(context.Background(), api.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = iss.Stop(context.Background()) })
	return iss
}

// httpGet issues an HTTP GET to url, returning status, headers, body. Tests
// assert on these directly. The transport disables redirects so /authorize
// 302 responses are observable.
func httpGet(t *testing.T, target string) (int, http.Header, string) {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

// httpPostForm issues an HTTP POST with form-encoded body. Optional
// Authorization header is set when basicAuth is non-nil. Redirects are not
// followed so callers can observe 302 responses directly (e.g. POST /authorize).
func httpPostForm(t *testing.T, target string, form url.Values, basicAuth *struct{ User, Pass string }) (int, http.Header, string) {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicAuth != nil {
		req.SetBasicAuth(basicAuth.User, basicAuth.Pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}
