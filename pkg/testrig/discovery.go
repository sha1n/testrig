package testrig

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// envDiscovery is a DiscoveryProvider backed by a DiscoveryStore.
// Use NewDiscovery or NewCrossProcessDiscovery to create instances.
type envDiscovery struct {
	store DiscoveryStore
}

// NewDiscovery creates a DiscoveryProvider backed by the given store.
// Returns a DiscoveryProvider; the concrete type is an implementation detail.
// Panics if store is nil.
func NewDiscovery(store DiscoveryStore) DiscoveryProvider {
	if store == nil {
		panic("testrig: NewDiscovery requires a non-nil DiscoveryStore")
	}
	return &envDiscovery{store: store}
}

// NewCrossProcessDiscovery creates a DiscoveryProvider backed by OS environment
// variables, suitable for cross-process service reuse.
func NewCrossProcessDiscovery() DiscoveryProvider {
	return NewDiscovery(NewOsEnvStore())
}

func (d *envDiscovery) Discover(ctx context.Context, svc Service) (Properties, bool, error) {
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	val, ok := d.store.Load(key)
	if !ok || val == "" {
		return nil, false, nil
	}

	props := make(Properties)
	if err := json.Unmarshal([]byte(val), &props); err != nil {
		// If it's not JSON, it might be the old "active" marker.
		// Return empty properties but indicate it was found.
		return make(Properties), true, nil
	}

	// Liveness check: verify the discovered service is actually running.
	// Known limitation: uses a hardcoded 2s timeout, not the caller's context deadline.
	// Callers who need fast cancellation should cancel the parent Start() context,
	// which short-circuits at the dependency-wait level.
	if !livenessCheck(props, svc.Name()) {
		return nil, false, nil
	}

	return props, true, nil
}

func (d *envDiscovery) Publish(ctx context.Context, svc Service, props Properties) error {
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	data, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("failed to marshal properties: %w", err)
	}
	if err := d.store.Store(key, string(data)); err != nil {
		return fmt.Errorf("failed to store discovery data for %s: %w", svc.Name(), err)
	}
	return nil
}

// Unpublish removes the service from the discovery registry.
// Called after a service is explicitly stopped to prevent dead-reuse.
func (d *envDiscovery) Unpublish(ctx context.Context, svc Service) error {
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	if err := d.store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete discovery data for %s: %w", svc.Name(), err)
	}
	return nil
}

// livenessCheck attempts a TCP dial to verify the discovered service is actually running.
// It uses the well-known "<svcName>.host" and "<svcName>.port" property keys.
// If those keys are not present, the check is skipped (backwards-compatible).
func livenessCheck(props Properties, svcName string) bool {
	host, hasHost := props[svcName+".host"]
	port, hasPort := props[svcName+".port"]
	if !hasHost || !hasPort {
		return true // No address to check; assume alive.
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
