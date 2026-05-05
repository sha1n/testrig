package testrig

import (
	"log/slog"
	"sync"
	"time"
)

// EnvContext provides read-only access to properties of started services and
// the per-service scoped logger. Service.Start and LifecycleHook callbacks
// receive one. Reads are safe to call concurrently with sibling services
// publishing properties.
type EnvContext interface {
	// Get returns the value for the given key, or "" and false if absent.
	Get(key string) (string, bool)
	// Int returns the value for the given key parsed as an int.
	Int(key string) (int, error)
	// Bool returns the value for the given key parsed as a bool.
	Bool(key string) (bool, error)
	// Duration returns the value for the given key parsed as a time.Duration.
	Duration(key string) (time.Duration, error)
	// Properties returns a copy of all properties currently visible.
	Properties() Properties
	// Logger returns a logger scoped for the current call site (per-service
	// during Start, env-level during hooks).
	Logger() *slog.Logger
}

// envContext is the EnvContext implementation passed to Service.Start and
// LifecycleHook callbacks. It borrows the parent Env's RWMutex via pointer
// so reads on `properties` synchronize with concurrent writes by sibling
// services; the zero value of sync.RWMutex on a value field would not share
// state. Hook envContexts are constructed over a snapshot map and so are
// effectively immutable.
type envContext struct {
	properties Properties
	mu         *sync.RWMutex
	logger     *slog.Logger
}

// newEnvContext constructs an envContext. Pass `mu` from the owning Env so
// reads on the live `properties` map synchronize with sibling-service
// writes. For hook contexts where `properties` is a stable snapshot, the
// mu argument is still passed (to satisfy the interface) but no concurrent
// writer exists.
func newEnvContext(properties Properties, mu *sync.RWMutex, logger *slog.Logger) *envContext {
	return &envContext{properties: properties, mu: mu, logger: logger}
}

func (c *envContext) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.properties[key]
	return val, ok
}

func (c *envContext) Int(key string) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.properties.Int(key)
}

func (c *envContext) Bool(key string) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.properties.Bool(key)
}

func (c *envContext) Duration(key string) (time.Duration, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.properties.Duration(key)
}

func (c *envContext) Properties() Properties {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.properties.snapshot()
}

func (c *envContext) Logger() *slog.Logger {
	return c.logger
}
