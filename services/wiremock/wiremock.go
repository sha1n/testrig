// Package wiremock provides a WireMock service backed by Testcontainers.
// The exported WireMock type implements testrig.Service.
package wiremock

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sha1n/testrig"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/wiremock/go-wiremock"
)

const (
	defaultImage = "wiremock/wiremock"
	defaultTag   = "3.2.0"

	// containerPort is the port WireMock listens on inside the container.
	containerPort = "8080"
)

// WireMock is a pre-configured WireMock test harness. It implements
// testrig.Service so it can be added to a testrig.Env, and exposes typed-client
// accessors (URL, Client) usable once the env has started.
//
// Construct with New, configure via the With* methods (chainable), then pass
// to env.With(...). A WireMock instance is reusable across Start/Stop cycles:
// Stop releases the container so a subsequent Start builds a fresh one.
// Calling Start without Stop in between returns an error.
//
// The service URL is published under "<name>.url" by default; override the
// key via WithURLPropertyName so tests can wire it directly into the
// application's expected config key.
type WireMock struct {
	name   string
	image  string
	tag    string
	logger *slog.Logger

	urlPropName string

	// Runtime state, populated during Start and cleared by Stop.
	// container != nil is the canonical "currently running" check.
	container testcontainers.Container
	url       string
}

// New creates a WireMock service with default configuration.
func New(name string) *WireMock {
	return &WireMock{
		name:        name,
		image:       defaultImage,
		tag:         defaultTag,
		logger:      slog.Default(),
		urlPropName: name + ".url",
	}
}

// WithImage sets the Docker image name.
func (t *WireMock) WithImage(image string) *WireMock { t.image = image; return t }

// WithTag sets the Docker image tag.
func (t *WireMock) WithTag(tag string) *WireMock { t.tag = tag; return t }

// WithURLPropertyName sets the property key under which the service URL is
// published. Default: "<name>.url".
func (t *WireMock) WithURLPropertyName(name string) *WireMock {
	t.urlPropName = name
	return t
}

// Name implements testrig.Service.
func (t *WireMock) Name() string { return t.name }

// Start implements testrig.Service. Returns an error if called while a
// previous Start is still active (i.e. Stop has not been called).
func (t *WireMock) Start(ctx context.Context, logger *slog.Logger) (testrig.Properties, error) {
	if t.container != nil {
		return nil, fmt.Errorf("wiremock service %q already started", t.name)
	}
	t.logger = logger
	t.logger.Info("🎬 Starting WireMock service", "name", t.name)

	container, err := testcontainers.Run(ctx,
		fmt.Sprintf("%s:%s", t.image, t.tag),
		testcontainers.WithExposedPorts(containerPort+"/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/__admin").
				WithPort(containerPort+"/tcp").
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start wiremock container: %w", err)
	}
	t.container = container

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock host: %w", err)
	}
	port, err := container.MappedPort(ctx, containerPort+"/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock mapped port: %w", err)
	}
	t.url = fmt.Sprintf("http://%s:%s", host, port.Port())

	return testrig.Properties{
		t.urlPropName: t.url,
	}, nil
}

// Stop implements testrig.Service. Safe to call before Start or twice in
// a row. Releases the container and clears runtime state so the service can
// be Started again.
func (t *WireMock) Stop(ctx context.Context) error {
	if t.container == nil {
		return nil
	}
	t.logger.Info("🛑 Stopping WireMock service", "name", t.name)
	err := t.container.Terminate(ctx)
	t.container = nil
	t.url = ""
	return err
}

// URL returns the WireMock service base URL. Only valid after Start.
func (t *WireMock) URL() string { return t.url }

// Client returns a WireMock client ready for fluent stubbing. Only valid after Start.
func (t *WireMock) Client() *wiremock.Client { return wiremock.NewClient(t.url) }
