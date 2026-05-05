package testrig

import (
	"log/slog"
	"sync"
	"time"
)

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
