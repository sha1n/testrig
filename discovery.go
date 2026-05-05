package testrig

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// DiscoveryProvider abstracts how a Service published by one Env (or one
// process) becomes discoverable to a later Env. Implementations may be
// in-process, OS-environment-backed, or backed by an external store.
type DiscoveryProvider interface {
	// Discover attempts to locate a running service matching svc.Identifier()
	// and return its previously-published properties. The bool reports
	// whether a match was found (and is alive, where applicable).
	Discover(ctx context.Context, svc Service) (Properties, bool, error)
	// Publish records that svc is now running with the given properties so
	// future Discover calls can return them.
	Publish(ctx context.Context, svc Service, props Properties) error
	// Unpublish removes svc from the registry. Env.Stop calls this for
	// every service it stopped, so future envs do not reuse a stale entry.
	Unpublish(ctx context.Context, svc Service) error
}

// envDiscovery is a DiscoveryProvider backed by a DiscoveryStore.
// Use NewDiscovery or NewOsEnvDiscovery to create instances.
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

// NewOsEnvDiscovery creates a DiscoveryProvider backed by OS environment
// variables. Suitable for parent → child process discovery: services published
// by the test process are visible to subprocesses spawned afterward (which
// inherit the env). Not a mechanism for sharing services across sibling
// processes — env mutations do not flow sideways, so e.g. independent
// `go test` package binaries do not see each other's published services.
func NewOsEnvDiscovery() DiscoveryProvider {
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
		return nil, false, fmt.Errorf("failed to decode discovery data for %s: %w", svc.Name(), err)
	}

	// Liveness check: verify the discovered service is actually running.
	if !livenessCheck(ctx, props, svc.Name()) {
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

// livenessCheck attempts a TCP dial to verify the discovered service is actually
// running. It uses the well-known "<svcName>.host" and "<svcName>.port" property
// keys. If those keys are not present, the check is skipped.
//
// The dial respects the caller's context. A 2-second cap is also applied so an
// unbounded ctx cannot stall discovery on a slow-failing host; the effective
// timeout is the minimum of the ctx deadline and the cap.
func livenessCheck(ctx context.Context, props Properties, svcName string) bool {
	host, hasHost := props[svcName+".host"]
	port, hasPort := props[svcName+".port"]
	if !hasHost || !hasPort {
		return true // No address to check; assume alive.
	}
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
