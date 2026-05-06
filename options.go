package testrig

import (
	"fmt"
	"log/slog"
)

// envConfig holds the result of applying Options before Env construction.
type envConfig struct {
	name     string
	services []Service
	logger   *slog.Logger
	hooks    []LifecycleHook
}

// Option configures an Env at construction time. Options are applied in order
// by New; an option may return an error to reject invalid input.
type Option func(*envConfig) error

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
