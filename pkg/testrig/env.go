package testrig

import (
	"context"
	"encoding/json"
	"errors"
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
// Construct via New(opts...). All configuration is applied at construction
// time; mutating an Env after construction (or concurrently with Start) is
// a programmer error.
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
		properties:   make(Properties),
		reused:       make(map[string]bool),
		started:      make(map[string]bool),
		signals:      make(map[string]chan struct{}),
		state:        stateIdle,
		newDiscovery: cfg.newDiscovery,
		logger:       cfg.logger,
		hooks:        cfg.hooks,
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

// WithDiscovery replaces the discovery provider. Returns an error if d is nil.
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
	if d.store == nil {
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewDiscovery() or NewCrossProcessDiscovery()")
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
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewDiscovery() or NewCrossProcessDiscovery()")
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
		panic("testrig: envDiscovery requires a DiscoveryStore; use NewDiscovery() or NewCrossProcessDiscovery()")
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

func (e *Env) Start(ctx context.Context) error {
	if err := e.prepareStart(); err != nil {
		return err
	}

	g, pCtx := errgroup.WithContext(ctx)
	for _, s := range e.services {
		svc := s
		g.Go(func() error { return e.startService(pCtx, svc) })
	}
	if err := g.Wait(); err != nil {
		_ = e.Stop(context.Background())
		return err
	}

	e.mu.Lock()
	e.state = stateRunning
	e.mu.Unlock()

	if err := e.runOnStartHooks(ctx); err != nil {
		_ = e.Stop(context.Background())
		return err
	}
	return nil
}

// prepareStart transitions the env to stateStarting under lock, validates
// configuration, and resets ephemeral runtime state for the upcoming Start.
// Returns an error (without changing state) if the env is not idle or if
// service configuration is invalid.
func (e *Env) prepareStart() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != stateIdle {
		return fmt.Errorf("environment %s is already running or starting", e.name)
	}
	if err := e.validateServiceNames(); err != nil {
		return err
	}
	if err := e.validateDependencies(); err != nil {
		return err
	}

	e.state = stateStarting

	// Create discovery from the factory so default envs get an isolated
	// MapStore per Start() while explicitly-shared providers are reused.
	e.discovery = e.newDiscovery()
	e.properties = make(Properties)
	e.reused = make(map[string]bool)
	e.started = make(map[string]bool)
	e.signals = make(map[string]chan struct{}, len(e.services))
	for _, s := range e.services {
		e.signals[s.Name()] = make(chan struct{})
	}
	return nil
}

func (e *Env) validateServiceNames() error {
	seen := make(map[string]bool, len(e.services))
	for _, s := range e.services {
		if seen[s.Name()] {
			return fmt.Errorf("duplicate service name: %s", s.Name())
		}
		seen[s.Name()] = true
	}
	return nil
}

// startService runs the full lifecycle for a single service: wait for its
// declared dependencies to start, try discovery/reuse, otherwise call its
// Start, then publish properties. Closes the service's signal on completion.
func (e *Env) startService(pCtx context.Context, svc Service) error {
	svcLogger := ScopedLogger(e.logger, svc.Name())
	svcEnvCtx := &envContext{properties: e.properties, mu: &e.mu, logger: svcLogger}

	if err := e.waitForDependencies(pCtx, svc); err != nil {
		return err
	}

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

	props, err := svc.Start(pCtx, svcEnvCtx)
	if err != nil {
		return fmt.Errorf("failed to start service %s: %w", svc.Name(), err)
	}

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
}

// waitForDependencies blocks until every dependency's start signal is closed
// or the parent context is canceled. Validity of dependency names is
// guaranteed by dag.Validate in prepareStart, so a missing entry here is a
// programmer error (not user input).
func (e *Env) waitForDependencies(pCtx context.Context, svc Service) error {
	for _, depName := range svc.Dependencies() {
		select {
		case <-e.signals[depName]:
		case <-pCtx.Done():
			return pCtx.Err()
		}
	}
	return nil
}

// runOnStartHooks invokes all registered LifecycleHook.OnStart callbacks with
// a stable property snapshot taken after all services started. The snapshot
// shields hooks from any later property mutations (notably, the clear in Stop).
func (e *Env) runOnStartHooks(ctx context.Context) error {
	e.mu.RLock()
	propSnapshot := e.properties.snapshot()
	e.mu.RUnlock()

	envCtx := &envContext{properties: propSnapshot, mu: &e.mu, logger: e.logger}
	for _, hook := range e.hooks {
		if err := hook.OnStart(ctx, envCtx); err != nil {
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

	if err := dag.Validate(nodes); err != nil {
		var udErr *dag.UnknownDepError
		if errors.As(err, &udErr) {
			return fmt.Errorf("service %s depends on unknown service %s", udErr.Node, udErr.MissingDep)
		}
		return err
	}
	return nil
}

type serviceNode struct {
	Service
}

func (s serviceNode) ID() string {
	return s.Name()
}
