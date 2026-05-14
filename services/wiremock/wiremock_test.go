package wiremock_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/sha1n/testrig"
	"github.com/sha1n/testrig/services/wiremock"
)

func TestWireMock_Defaults(t *testing.T) {
	tk := wiremock.New("test-mock")

	if tk.Name() != "test-mock" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}

	props, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	url, ok := props["test-mock.url"]
	if !ok || !strings.Contains(url, "http://localhost:") {
		t.Errorf("Expected URL property to start with http://localhost:, got %s", url)
	}
}

func TestWireMock_Configured(t *testing.T) {
	tk := wiremock.New("custom-mock").
		WithImage("wiremock/wiremock").
		WithTag("3.3.1")

	props, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if url := props["custom-mock.url"]; url == "" {
		t.Error("Expected url property to be populated")
	}
}

func TestWireMock_URLPropertyName_Override(t *testing.T) {
	tk := wiremock.New("wm").WithURLPropertyName("MOCK_URL")

	props, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if props["MOCK_URL"] == "" {
		t.Error("MOCK_URL property not published under custom key")
	}
	if _, ok := props["wm.url"]; ok {
		t.Error("default key wm.url should not be published when overridden")
	}
}

func TestWireMock_StartTwice_ReturnsError(t *testing.T) {
	tk := wiremock.New("twice")
	if _, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if _, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err == nil {
		t.Error("Expected error on second Start")
	}
}

func TestWireMock_StopThenStart_Succeeds(t *testing.T) {
	// A service instance must be reusable across env restart cycles. Stop
	// releases the container and clears service state so a subsequent Start
	// builds a fresh one.
	tk := wiremock.New("restart-test")
	ctx := context.Background()

	if _, err := tk.Start(ctx, testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if _, err := tk.Start(ctx, testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("second Start after Stop must succeed; got: %v", err)
	}
	if err := tk.Stop(ctx); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestWireMock_Start_Error(t *testing.T) {
	tk := wiremock.New("err-wm").WithImage("non-existent-image-12345")
	_, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
}

func TestWireMock_Stop_NoContainer(t *testing.T) {
	tk := wiremock.New("no-container")
	if err := tk.Stop(context.Background()); err != nil {
		t.Errorf("Stop without container should be no-op, got %v", err)
	}
}

func TestWireMock_URL_MatchesProperty(t *testing.T) {
	tk := wiremock.New("url-match")

	props, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.URL() != props["url-match.url"] {
		t.Errorf("URL() and url-match.url property should match. URL()=%s prop=%s", tk.URL(), props["url-match.url"])
	}
}

func TestWireMock_Client_NotNil(t *testing.T) {
	tk := wiremock.New("client-test")

	if _, err := tk.Start(context.Background(), testrig.StubEnvHandle("test", slog.Default(), nil)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.Client() == nil {
		t.Error("Client() returned nil")
	}
}
