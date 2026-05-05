package wiremock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sha1n/testrig-go/internal/testutil"
	"github.com/sha1n/testrig-go/pkg/testrig/testkits/wiremock"
)

func TestTestkit_Defaults(t *testing.T) {
	tk := wiremock.New("test-mock")

	if tk.Name() != "test-mock" {
		t.Errorf("Unexpected name: %s", tk.Name())
	}
	if !strings.HasPrefix(tk.Identifier(), "wiremock:") {
		t.Errorf("Unexpected identifier prefix: %s", tk.Identifier())
	}
	if len(tk.Dependencies()) != 0 {
		t.Error("Expected no dependencies")
	}

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	url, ok := props["test-mock.url"]
	if !ok || !strings.Contains(url, "http://localhost:") {
		t.Errorf("Expected URL property to start with http://localhost:, got %s", url)
	}
}

func TestTestkit_Configured(t *testing.T) {
	tk := wiremock.New("custom-mock").
		WithImage("wiremock/wiremock").
		WithTag("3.3.1")

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if url := props["custom-mock.url"]; url == "" {
		t.Error("Expected url property to be populated")
	}
}

func TestTestkit_Identifier_StableAndCollisionResistant(t *testing.T) {
	a := wiremock.New("svc")
	b := wiremock.New("svc")
	c := wiremock.New("svc").WithTag("3.3.1")

	if a.Identifier() != b.Identifier() {
		t.Error("Same config should yield same identifier")
	}
	if a.Identifier() == c.Identifier() {
		t.Error("Different tag should yield different identifier")
	}
}

func TestTestkit_StartTwice_ReturnsError(t *testing.T) {
	tk := wiremock.New("twice")
	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err == nil {
		t.Error("Expected error on second Start")
	}
}

func TestTestkit_Start_Error(t *testing.T) {
	tk := wiremock.New("err-wm").WithImage("non-existent-image-12345")
	_, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
}

func TestTestkit_Stop_NoContainer(t *testing.T) {
	tk := wiremock.New("no-container")
	if err := tk.Stop(context.Background()); err != nil {
		t.Errorf("Stop without container should be no-op, got %v", err)
	}
}

func TestTestkit_URL_MatchesProperty(t *testing.T) {
	tk := wiremock.New("url-match")

	props, err := tk.Start(context.Background(), &testutil.MockEnvContext{})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.URL() != props["url-match.url"] {
		t.Errorf("URL() and url-match.url property should match. URL()=%s prop=%s", tk.URL(), props["url-match.url"])
	}
}

func TestTestkit_Client_NotNil(t *testing.T) {
	tk := wiremock.New("client-test")

	if _, err := tk.Start(context.Background(), &testutil.MockEnvContext{}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = tk.Stop(context.Background()) }()

	if tk.Client() == nil {
		t.Error("Client() returned nil")
	}
}
