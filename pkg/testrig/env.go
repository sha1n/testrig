package testrig

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/sha1n/testrig-go/internal/dag"
	"golang.org/x/sync/errgroup"
)

type envContext struct {
	properties Properties
	mu         *sync.RWMutex
	logger     *slog.Logger
}

type envState int

const (
	stateIdle envState = iota
	stateStarting
	stateRunning
)

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
	p := make(Properties)
	for k, v := range c.properties {
		p[k] = v
	}
	return p
}

func (c *envContext) Logger() *slog.Logger {
	return c.logger
}

// Env manages a set of Service implementations, handling their lifecycle,
// dependency resolution, and property propagation.
//
// All builder methods (With, WithDiscovery, WithLogger, WithHooks) must be
// called before Start(). Calling builders concurrently with Start() or while
// the env is running is a programmer error and results in undefined behavior.
type Env struct {
	name       string
	services   []Service
	properties Properties
	reused     map[string]bool
	started    map[string]bool
	mu         sync.RWMutex
	// Channels to signal when a service has started
	signals      map[string]chan struct{}
	state        envState
	discovery    DiscoveryProvider        // set by Start() from newDiscovery; used by Start/Stop
	newDiscovery func() DiscoveryProvider // factory — default creates isolated MapStore per Start()
	logger       *slog.Logger
	hooks        []LifecycleHook
}

// New creates a new Env with isolation-safe defaults: in-process MapStore
// for discovery (no OS env pollution) and slog.Default() logger.
func New() *Env {
	return &Env{
		name:         "testenv",
		properties:   make(Properties),
		reused:       make(map[string]bool),
		started:      make(map[string]bool),
		signals:      make(map[string]chan struct{}),
		state:        stateIdle,
		newDiscovery: func() DiscoveryProvider { return NewEnvDiscovery(NewMapStore()) },
		logger:       slog.Default(),
	}
}

// shallowCopy returns a new *Env with all configuration fields copied.
// The mutex and ephemeral runtime state (properties, reused, started, signals)
// are NOT copied — they are always freshly initialised on Start(). This avoids
// copying a sync.RWMutex (which would be a data-race and a lint error).
func (e *Env) shallowCopy() *Env {
	return &Env{
		name:         e.name,
		services:     e.services,
		newDiscovery: e.newDiscovery,
		logger:       e.logger,
		hooks:        e.hooks,
		// mu, state, properties, reused, started, signals, discovery — zero values are correct.
		// discovery is set by Start() from newDiscovery.
	}
}

// WithName sets a custom name for the environment, used in logs and error messages.
// Returns a new *Env; the receiver is not modified.
// Panics if name is empty.
func (e *Env) WithName(name string) *Env {
	if name == "" {
		panic("testrig: WithName requires a non-empty name")
	}
	cp := e.shallowCopy()
	cp.name = name
	return cp
}

// WithDiscovery replaces the discovery provider.
// Returns a new *Env; the receiver is not modified.
// Panics if d is nil.
func (e *Env) WithDiscovery(d DiscoveryProvider) *Env {
	if d == nil {
		panic("testrig: WithDiscovery requires a non-nil DiscoveryProvider")
	}
	cp := e.shallowCopy()
	cp.newDiscovery = func() DiscoveryProvider { return d }
	return cp
}

// WithLogger replaces the logger for the environment.
// Returns a new *Env; the receiver is not modified.
// Panics if logger is nil.
func (e *Env) WithLogger(logger *slog.Logger) *Env {
	if logger == nil {
		panic("testrig: WithLogger requires a non-nil *slog.Logger")
	}
	cp := e.shallowCopy()
	cp.logger = logger
	return cp
}

// WithHooks appends lifecycle hooks to the environment (accumulative).
// Returns a new *Env; the receiver is not modified.
// Panics if any hook is nil.
func (e *Env) WithHooks(hooks ...LifecycleHook) *Env {
	for i, h := range hooks {
		if h == nil {
			panic(fmt.Sprintf("testrig: WithHooks received a nil LifecycleHook at index %d", i))
		}
	}
	cp := e.shallowCopy()
	cp.hooks = append(append([]LifecycleHook(nil), e.hooks...), hooks...)
	return cp
}

// envDiscovery is a DiscoveryProvider backed by a DiscoveryStore.
// Use NewEnvDiscovery or NewCrossProcessDiscovery to create instances.
type envDiscovery struct {
	store DiscoveryStore
}

// NewEnvDiscovery creates a DiscoveryProvider backed by the given store.
// Returns a DiscoveryProvider; the concrete type is an implementation detail.
// Panics if store is nil.
func NewEnvDiscovery(store DiscoveryStore) DiscoveryProvider {
	if store == nil {
		panic("testrig: NewEnvDiscovery requires a non-nil DiscoveryStore")
	}
	return &envDiscovery{store: store}
}

// NewCrossProcessDiscovery creates a DiscoveryProvider backed by OS environment
// variables, suitable for cross-process service reuse.
func NewCrossProcessDiscovery() DiscoveryProvider {
	return NewEnvDiscovery(NewOsEnvStore())
}

func (d *envDiscovery) Discover(ctx context.Context, svc Service) (Properties, bool, error) {
	if d.store == nil {
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewEnvDiscovery() or NewCrossProcessDiscovery()")
	}
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

func (d *envDiscovery) Publish(ctx context.Context, svc Service, props Properties) error {
	if d.store == nil {
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewEnvDiscovery() or NewCrossProcessDiscovery()")
	}
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
	if d.store == nil {
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewEnvDiscovery() or NewCrossProcessDiscovery()")
	}
	key := "TESTRIG_SERVICE_" + svc.Identifier()
	if err := d.store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete discovery data for %s: %w", svc.Name(), err)
	}
	return nil
}

func (e *Env) Name() string {
	return e.name
}

// Properties returns a copy of all properties in the environment.
func (e *Env) Properties() Properties {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p := make(Properties)
	for k, v := range e.properties {
		p[k] = v
	}
	return p
}

// With appends one or more services to the environment (accumulative).
// Returns a new *Env; the receiver is not modified.
// Panics if any service is nil.
func (e *Env) With(services ...Service) *Env {
	for i, s := range services {
		if s == nil {
			panic(fmt.Sprintf("testrig: With received a nil Service at index %d", i))
		}
	}
	cp := e.shallowCopy()
	cp.services = append(append([]Service(nil), e.services...), services...)
	return cp
}

func (e *Env) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.state != stateIdle {
		e.mu.Unlock()
		return fmt.Errorf("environment %s is already running or starting", e.name)
	}
	e.state = stateStarting

	// Validate duplicate service names
	seen := make(map[string]bool, len(e.services))
	for _, s := range e.services {
		if seen[s.Name()] {
			e.state = stateIdle
			e.mu.Unlock()
			return fmt.Errorf("duplicate service name: %s", s.Name())
		}
		seen[s.Name()] = true
	}

	if err := e.validateDependencies(); err != nil {
		e.state = stateIdle
		e.mu.Unlock()
		return err
	}

	// Reset ephemeral state for reusability.
	// Create discovery from the factory so default envs get an isolated MapStore
	// per Start() while explicitly-shared providers are reused as intended.
	e.discovery = e.newDiscovery()
	e.properties = make(Properties)
	e.reused = make(map[string]bool)
	e.started = make(map[string]bool)
	e.signals = make(map[string]chan struct{})
	for _, s := range e.services {
		e.signals[s.Name()] = make(chan struct{})
	}
	e.mu.Unlock()

	g, pCtx := errgroup.WithContext(ctx)

	for _, s := range e.services {
		svc := s
		g.Go(func() error {
			// Task 3.1 — Per-service scoped logger so logs can be filtered by service name.
			svcLogger := ScopedLogger(e.logger, svc.Name())
			svcEnvCtx := &envContext{properties: e.properties, mu: &e.mu, logger: svcLogger}

			// 1. Wait for dependencies
			for _, depName := range svc.Dependencies() {
				sig, ok := e.signals[depName]
				if !ok {
					return fmt.Errorf("service %s depends on unknown service %s", svc.Name(), depName)
				}
				select {
				case <-sig:
					// Dependency ready
				case <-pCtx.Done():
					return pCtx.Err()
				}
			}

			// 2. Try discovery/reuse
			discoveredProps, found, err := e.discovery.Discover(pCtx, svc)
			if err != nil {
				return fmt.Errorf("discovery failed for service %s: %w", svc.Name(), err)
			}
			if found {
				svcLogger.Info("♻️  Reusing service", "name", svc.Name())
				e.mu.Lock()
				e.reused[svc.Identifier()] = true
				for k, v := range discoveredProps {
					e.properties[k] = v
				}
				e.mu.Unlock()
				close(e.signals[svc.Name()])
				return nil
			}

			// 3. Start service
			props, err := svc.Start(pCtx, svcEnvCtx)
			if err != nil {
				return fmt.Errorf("failed to start service %s: %w", svc.Name(), err)
			}

			// 4. Update environment and signal completion
			e.mu.Lock()
			e.started[svc.Name()] = true
			for k, v := range props {
				e.properties[k] = v
			}
			e.mu.Unlock()

			if err := e.discovery.Publish(pCtx, svc, props.snapshot()); err != nil {
				return fmt.Errorf("failed to publish service %s: %w", svc.Name(), err)
			}

			close(e.signals[svc.Name()])
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// Rollback started services
		_ = e.Stop(context.Background())
		return err
	}

	// Take a snapshot of properties now that all services have started.
	// The snapshot is passed to hooks so they hold a stable, immutable view
	// and are not affected by any concurrent mutations or the property-clear
	// that happens in Stop().
	e.mu.RLock()
	propSnapshot := e.properties.snapshot()
	e.mu.RUnlock()

	e.mu.Lock()
	e.state = stateRunning
	e.mu.Unlock()

	envCtx := &envContext{properties: propSnapshot, mu: &e.mu, logger: e.logger}
	for _, hook := range e.hooks {
		if err := hook.OnStart(ctx, envCtx); err != nil {
			_ = e.Stop(context.Background())
			return fmt.Errorf("lifecycle hook OnStart failed: %w", err)
		}
	}

	return nil
}

func (e *Env) Stop(ctx context.Context) error {
	e.mu.Lock()
	if e.state != stateRunning && e.state != stateStarting && len(e.started) == 0 {
		e.mu.Unlock()
		return nil // Already stopped or idle with no started services
	}
	e.mu.Unlock()

	// Invert dependencies to find what depends on what
	dependents := make(map[string][]string)
	for _, s := range e.services {
		for _, dep := range s.Dependencies() {
			dependents[dep] = append(dependents[dep], s.Name())
		}
	}

	stopSignals := make(map[string]chan struct{})
	for _, s := range e.services {
		stopSignals[s.Name()] = make(chan struct{})
	}

	g, pCtx := errgroup.WithContext(ctx)
	for _, s := range e.services {
		svc := s
		g.Go(func() error {
			defer close(stopSignals[svc.Name()])

			// 1. Wait for all services that depend on this one to stop first
			for _, depName := range dependents[svc.Name()] {
				sig, ok := stopSignals[depName]
				if !ok {
					continue
				}
				select {
				case <-sig:
					// Dependent stopped
				case <-pCtx.Done():
					return pCtx.Err()
				}
			}

			// 2. Stop this service if it was started (not reused)
			e.mu.Lock()
			wasStarted := e.started[svc.Name()]
			e.mu.Unlock()

			if wasStarted {
				if err := svc.Stop(pCtx); err != nil {
					return fmt.Errorf("failed to stop service %s: %w", svc.Name(), err)
				}
				e.mu.Lock()
				delete(e.started, svc.Name())
				e.mu.Unlock()
				// Task 1.3 — Unpublish: remove from discovery so the stopped service isn't reused.
				if err := e.discovery.Unpublish(pCtx, svc); err != nil {
					return fmt.Errorf("failed to unpublish service %s: %w", svc.Name(), err)
				}
			}

			return nil
		})
	}

	err := g.Wait()

	if len(e.hooks) > 0 {
		// Snapshot properties before clearing them, so OnStop hooks see a stable
		// immutable view of the properties that were active during the run.
		e.mu.RLock()
		propSnapshot := e.properties.snapshot()
		e.mu.RUnlock()
		envCtx := &envContext{properties: propSnapshot, mu: &e.mu, logger: e.logger}
		for _, hook := range e.hooks {
			if pmErr := hook.OnStop(ctx, envCtx); pmErr != nil {
				if err == nil {
					err = fmt.Errorf("lifecycle hook OnStop failed: %w", pmErr)
				} else {
					e.logger.Error("lifecycle hook OnStop failed", "error", pmErr)
				}
			}
		}
	}

	e.mu.Lock()
	e.state = stateIdle
	e.properties = make(Properties)
	e.reused = make(map[string]bool)
	e.started = make(map[string]bool)
	e.mu.Unlock()

	return err
}

func (e *Env) validateDependencies() error {
	nodes := make([]dag.Node, len(e.services))
	for i, s := range e.services {
		nodes[i] = serviceNode{s}
	}

	return dag.Validate(nodes)
}

type serviceNode struct {
	Service
}

func (s serviceNode) ID() string {
	return s.Name()
}
