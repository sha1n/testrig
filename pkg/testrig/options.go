package testrig

import (
	"fmt"
	"log/slog"
)

// envConfig holds the result of applying Options before Env construction.
type envConfig struct {
	name         string
	services     []Service
	newDiscovery func() DiscoveryProvider
	logger       *slog.Logger
	hooks        []LifecycleHook
}

// Option configures an Env at construction time. Options are applied in order
// by New; an option may return an error to reject invalid input.
type Option func(*envConfig) error

// New creates a new Env with isolation-safe defaults (in-process MapStore for
// discovery, slog.Default() logger), then applies the given options. Returns
// an error if any option rejects its input.
func New(opts ...Option) (*Env, error) {
	cfg := envConfig{
		name:         "testenv",
		newDiscovery: func() DiscoveryProvider { return NewDiscovery(NewMapStore()) },
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	return &Env{
		name:         cfg.name,
		services:     cfg.services,
		newDiscovery: cfg.newDiscovery,
		logger:       cfg.logger,
		hooks:        cfg.hooks,
		state:        stateIdle,
	}, nil
}

// MustNew is like New but panics on error. Convenient for tests and other
// places where invalid configuration is a static, programmer-checked condition.
func MustNew(opts ...Option) *Env {
	env, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return env
}

// WithName sets a custom name for the environment, used in logs and error
// messages. Returns an error if name is empty.
func WithName(name string) Option {
	return func(c *envConfig) error {
		if name == "" {
			return fmt.Errorf("testrig: WithName requires a non-empty name")
		}
		c.name = name
		return nil
	}
}

// WithDiscovery replaces the discovery provider. The same provider is reused
// across every Start of this Env — that is the point of supplying one
// explicitly, since cross-process and cross-env reuse rely on a stable
// underlying store.
//
// Contrast with the default: New() without WithDiscovery installs a factory
// that produces a fresh in-process MapStore per Start, so independent envs
// are isolated by construction.
//
// Returns an error if d is nil.
func WithDiscovery(d DiscoveryProvider) Option {
	return func(c *envConfig) error {
		if d == nil {
			return fmt.Errorf("testrig: WithDiscovery requires a non-nil DiscoveryProvider")
		}
		c.newDiscovery = func() DiscoveryProvider { return d }
		return nil
	}
}

// WithLogger replaces the logger for the environment. Returns an error if
// logger is nil.
func WithLogger(logger *slog.Logger) Option {
	return func(c *envConfig) error {
		if logger == nil {
			return fmt.Errorf("testrig: WithLogger requires a non-nil *slog.Logger")
		}
		c.logger = logger
		return nil
	}
}

// WithHooks appends lifecycle hooks (accumulative across calls). Returns an
// error if any hook is nil.
func WithHooks(hooks ...LifecycleHook) Option {
	return func(c *envConfig) error {
		for i, h := range hooks {
			if h == nil {
				return fmt.Errorf("testrig: WithHooks received a nil LifecycleHook at index %d", i)
			}
		}
		c.hooks = append(c.hooks, hooks...)
		return nil
	}
}

// With appends services (accumulative across calls). Returns an error if any
// service is nil.
func With(services ...Service) Option {
	return func(c *envConfig) error {
		for i, s := range services {
			if s == nil {
				return fmt.Errorf("testrig: With received a nil Service at index %d", i)
			}
		}
		c.services = append(c.services, services...)
		return nil
	}
}
