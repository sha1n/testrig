package testrig

import (
	"os"
	"sync"
)

// DiscoveryStore abstracts the storage backend used by the discovery provider for
// persisting and retrieving service discovery data. Two implementations are
// provided out of the box: NewMapStore (in-process, test-isolated) and
// NewOsEnvStore (cross-process via OS environment variables).
type DiscoveryStore interface {
	// Load retrieves the value associated with key.
	// Returns ("", false) if the key is not present.
	Load(key string) (string, bool)
	// Store persists a key-value pair.
	Store(key, value string) error
	// Delete removes a key from the store.
	Delete(key string) error
}

var (
	_ DiscoveryStore = (*mapStore)(nil)
	_ DiscoveryStore = (*osEnvStore)(nil)
)

// mapStore is an in-process DiscoveryStore backed by a map.
// It is safe for concurrent use. The zero value is usable but callers
// should prefer NewMapStore().
type mapStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewMapStore creates a ready-to-use in-process DiscoveryStore.
// Returns a DiscoveryStore; the concrete type is an implementation detail.
func NewMapStore() DiscoveryStore {
	return &mapStore{data: make(map[string]string)}
}

func (s *mapStore) Load(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Reading from a nil map returns the zero value — safe.
	val, ok := s.data[key]
	return val, ok
}

func (s *mapStore) Store(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string]string)
	}
	s.data[key] = value
	return nil
}

func (s *mapStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// delete on a nil map is a safe no-op in Go.
	delete(s.data, key)
	return nil
}

// osEnvStore is a DiscoveryStore that delegates to OS environment variables.
// It is intended for cross-process reuse scenarios where service discovery
// data must be visible to child processes.
//
// The OS environment is process-wide shared state, and the Go language spec
// does not guarantee that os.Setenv / os.Unsetenv are safe for concurrent
// use across all platforms. testrig serializes its own access through
// osEnvMu to give callers a portable concurrency contract; this does not
// protect against env mutations made outside testrig.
type osEnvStore struct{}

// osEnvMu serializes all reads and writes to the OS environment performed
// by osEnvStore. It is package-level because the OS environment is a shared
// process resource: multiple osEnvStore instances would otherwise race
// against each other.
var osEnvMu sync.Mutex

// NewOsEnvStore creates a DiscoveryStore backed by OS environment variables.
// Returns a DiscoveryStore; the concrete type is an implementation detail.
// Prefer NewCrossProcessDiscovery() unless you need to compose the store directly.
func NewOsEnvStore() DiscoveryStore {
	return &osEnvStore{}
}

func (s *osEnvStore) Load(key string) (string, bool) {
	osEnvMu.Lock()
	defer osEnvMu.Unlock()
	return os.LookupEnv(key)
}

func (s *osEnvStore) Store(key, value string) error {
	osEnvMu.Lock()
	defer osEnvMu.Unlock()
	return os.Setenv(key, value)
}

func (s *osEnvStore) Delete(key string) error {
	osEnvMu.Lock()
	defer osEnvMu.Unlock()
	return os.Unsetenv(key)
}
