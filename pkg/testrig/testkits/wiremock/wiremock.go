// Package wiremock provides a Testkit backed by a Testcontainers WireMock
// container. The Testkit implements testrig.Service.
package wiremock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/sha1n/testrig-go/pkg/testrig"
	"github.com/wiremock/go-wiremock"
	wiremock_tc "github.com/wiremock/wiremock-testcontainers-go"
)

const (
	defaultImage = "wiremock/wiremock"
	defaultTag   = "3.2.0"
)

// Testkit is a pre-configured WireMock test harness. It implements
// testrig.Service so it can be added to a testrig.Env, and (in a later
// commit) exposes typed-client accessors usable once the env has started.
//
// Construct with New, configure via the With* methods (chainable), then pass
// to env.With(...). Calling Start more than once returns an error.
type Testkit struct {
	name   string
	image  string
	tag    string
	logger *slog.Logger

	// runtime state
	container *wiremock_tc.WireMockContainer
	url       string
	started   bool
}

// New creates a WireMock Testkit with default configuration.
func New(name string) *Testkit {
	return &Testkit{
		name:   name,
		image:  defaultImage,
		tag:    defaultTag,
		logger: slog.Default(),
	}
}

// WithImage sets the Docker image name.
func (t *Testkit) WithImage(image string) *Testkit { t.image = image; return t }

// WithTag sets the Docker image tag.
func (t *Testkit) WithTag(tag string) *Testkit { t.tag = tag; return t }

// Name implements testrig.Service.
func (t *Testkit) Name() string { return t.name }

// Identifier returns a content-addressed identifier over the testkit config.
func (t *Testkit) Identifier() string {
	parts := []string{"wiremock", t.image, t.tag, t.name}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "wiremock:" + hex.EncodeToString(sum[:])
}

// Dependencies implements testrig.Service. WireMock is a leaf service.
func (t *Testkit) Dependencies() []string { return nil }

// Start implements testrig.Service. Returns an error if called twice.
func (t *Testkit) Start(ctx context.Context, envCtx testrig.EnvContext) (testrig.Properties, error) {
	if t.started {
		return nil, fmt.Errorf("wiremock testkit %q already started", t.name)
	}
	t.logger = envCtx.Logger()
	t.logger.Info("🎬 Starting WireMock testkit", "name", t.name)

	container, err := wiremock_tc.RunContainer(ctx, wiremock_tc.WithImage(fmt.Sprintf("%s:%s", t.image, t.tag)))
	if err != nil {
		return nil, fmt.Errorf("failed to start wiremock container: %w", err)
	}
	t.container = container
	t.started = true

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock host: %w", err)
	}
	port, err := container.MappedPort(ctx, "8080")
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock mapped port: %w", err)
	}
	t.url = fmt.Sprintf("http://%s:%s", host, port.Port())

	return testrig.Properties{
		t.name + ".url": t.url,
	}, nil
}

// Stop implements testrig.Service. Safe to call before Start.
func (t *Testkit) Stop(ctx context.Context) error {
	t.logger.Info("🛑 Stopping WireMock testkit", "name", t.name)
	if t.container != nil {
		return t.container.Terminate(ctx)
	}
	return nil
}

// URL returns the WireMock service base URL. Only valid after Start.
func (t *Testkit) URL() string { return t.url }

// Client returns a WireMock client ready for fluent stubbing. Only valid after Start.
func (t *Testkit) Client() *wiremock.Client { return wiremock.NewClient(t.url) }
