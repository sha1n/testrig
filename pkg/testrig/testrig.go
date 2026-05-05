package testrig

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// Properties represents dynamic configuration produced by services.
type Properties map[string]string

// Int returns the value for the given key as an int.
func (p Properties) Int(key string) (int, error) {
	val, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("property %s not found", key)
	}
	return strconv.Atoi(val)
}

// Bool returns the value for the given key as a bool.
func (p Properties) Bool(key string) (bool, error) {
	val, ok := p[key]
	if !ok {
		return false, fmt.Errorf("property %s not found", key)
	}
	return strconv.ParseBool(val)
}

// Duration returns the value for the given key as a time.Duration.
func (p Properties) Duration(key string) (time.Duration, error) {
	val, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("property %s not found", key)
	}
	return time.ParseDuration(val)
}

// snapshot returns a deep copy of the properties map.
// Use this whenever a stable, immutable view is required (e.g. hook contexts,
// discovery publish calls) to prevent aliasing against the live internal map.
func (p Properties) snapshot() Properties {
	cp := make(Properties, len(p))
	for k, v := range p {
		cp[k] = v
	}
	return cp
}

// EnvContext provides read-only access to properties of started services.
type EnvContext interface {
	// Get returns the value for the given key if it exists.
	Get(key string) (string, bool)
	// Int returns the value for the given key as an int.
	Int(key string) (int, error)
	// Bool returns the value for the given key as a bool.
	Bool(key string) (bool, error)
	// Duration returns the value for the given key as a time.Duration.
	Duration(key string) (time.Duration, error)
	// Properties returns a copy of all properties available so far.
	Properties() Properties
	// Logger returns the logger for the environment.
	Logger() *slog.Logger
}

// Service represents a stateful dependency.
type Service interface {
	Name() string
	// Identifier returns a unique ID for this service configuration.
	// This is used for cross-process discovery.
	Identifier() string
	// Dependencies returns the names of services this service depends on.
	Dependencies() []string
	// Start starts the service and returns its properties.
	// It receives an EnvContext to access properties of services it depends on.
	Start(ctx context.Context, envCtx EnvContext) (Properties, error)
	// Stop stops the service.
	Stop(ctx context.Context) error
}

// DiscoveryProvider is an interface for finding already running services.
type DiscoveryProvider interface {
	// Discover attempts to find a running service and return its properties.
	Discover(ctx context.Context, svc Service) (Properties, bool, error)
	// Publish marks a service as running so it can be discovered later.
	Publish(ctx context.Context, svc Service, props Properties) error
	// Unpublish removes a service from the discovery registry.
	// It is called when a service is explicitly stopped.
	Unpublish(ctx context.Context, svc Service) error
}

// LifecycleHook is an interface that handles properties after successful service start.
// It can optionally be supplied to the Env and will be called after the environment
// has started and before it is stopped.
type LifecycleHook interface {
	// OnStart is called after all services in the environment have started successfully.
	// It can be used to set environment variables, create config files, etc.
	// If it returns an error, the environment startup fails.
	OnStart(ctx context.Context, envCtx EnvContext) error
	// OnStop is called after all services in the environment have stopped.
	// It can be used to clean up resources created in OnStart.
	// If it returns an error, the environment shutdown fails.
	OnStop(ctx context.Context, envCtx EnvContext) error
}
