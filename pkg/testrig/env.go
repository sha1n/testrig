package testrig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/sha1n/testrig-go/internal/dag"
	"golang.org/x/sync/errgroup"
)

// Env manages a set of Service implementations, handling their lifecycle,
// dependency resolution, and property propagation.
//
// Construct via New(opts...). All configuration is applied at construction
// time; mutating an Env after construction (or concurrently with Start) is
// a programmer error.
type Env struct {
	// Immutable after construction.
	name         string
	services     []Service
	newDiscovery func() DiscoveryProvider // factory — default creates an isolated MapStore per Start()
	logger       *slog.Logger
	hooks        []LifecycleHook

	// Mutable lifecycle state, guarded by mu.
	mu    sync.RWMutex
	state envState
	run   *runState // non-nil iff state != stateIdle
}

// runState holds the per-Start mutable state of an Env. It is allocated in
// prepareStart and released in Stop, so a single nil check (run == nil) tells
// us whether the env is idle. Fields are accessed under Env.mu.
type runState struct {
	properties Properties
	started    map[string]bool
	signals    map[string]chan struct{}
	discovery  DiscoveryProvider
}

type envState int

const (
	stateIdle envState = iota
	stateStarting
	stateRunning
	stateStopping
)

func (s envState) String() string {
	switch s {
	case stateIdle:
		return "idle"
	case stateStarting:
		return "starting"
	case stateRunning:
		return "running"
	case stateStopping:
		return "stopping"
	default:
		return fmt.Sprintf("envState(%d)", int(s))
	}
}

// scopedLogger returns a child logger with the given service name attribute.
// Used internally to scope per-service loggers; users get this scoped logger
// via EnvContext.Logger() and can compose further attributes through slog
// directly.
func scopedLogger(parent *slog.Logger, name string) *slog.Logger {
	return parent.With("service", name)
}

func (e *Env) Name() string {
	return e.name
}

// Properties returns a copy of all properties in the environment, or an empty
// map when the env is idle.
func (e *Env) Properties() Properties {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.run == nil {
		return make(Properties)
	}
	return e.run.properties.snapshot()
}

func (e *Env) Start(ctx context.Context) error {
	if err := e.prepareStart(); err != nil {
		return err
	}

	// prepareStart wrote run, signals, and properties under e.mu before
	// returning. The act of spawning these goroutines establishes the
	// happens-before edge for that write, so the workers can read those
	// fields without retaking the lock.
	g, pCtx := errgroup.WithContext(ctx)
	for _, s := range e.services {
		svc := s
		g.Go(func() error { return e.startService(pCtx, svc) })
	}
	if err := g.Wait(); err != nil {
		return errors.Join(err, e.Stop(context.Background()))
	}

	e.mu.Lock()
	e.state = stateRunning
	e.mu.Unlock()

	if err := e.runOnStartHooks(ctx); err != nil {
		return errors.Join(err, e.Stop(context.Background()))
	}
	return nil
}

// prepareStart transitions the env to stateStarting under lock, validates
// configuration, and allocates a fresh runState for the upcoming Start.
// Returns an error (without changing state) if the env is not idle or if
// service configuration is invalid.
func (e *Env) prepareStart() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != stateIdle {
		return fmt.Errorf("environment %s is %s, must be idle to start", e.name, e.state)
	}
	if err := validateServiceNames(e.services); err != nil {
		return err
	}
	if err := validateDependencies(e.services); err != nil {
		return err
	}

	// Create discovery from the factory so default envs get an isolated
	// MapStore per Start() while explicitly-shared providers are reused.
	run := &runState{
		properties: make(Properties),
		started:    make(map[string]bool, len(e.services)),
		signals:    make(map[string]chan struct{}, len(e.services)),
		discovery:  e.newDiscovery(),
	}
	for _, s := range e.services {
		run.signals[s.Name()] = make(chan struct{})
	}
	e.run = run
	e.state = stateStarting
	return nil
}

// startService runs the full lifecycle for a single service: wait for its
// declared dependencies to start, try discovery/reuse, otherwise call its
// Start, then publish properties.
//
// Signal contract: run.signals[svc.Name()] is closed only on full success
// (reuse or fresh-Start with successful Publish). On any error path the
// signal is intentionally left open. Dependents waiting on it unblock via
// pCtx.Done() instead, which errgroup cancels as soon as any goroutine
// returns an error. This keeps "closed signal == dependency is ready" as a
// strong invariant rather than just "this goroutine exited".
func (e *Env) startService(pCtx context.Context, svc Service) error {
	run := e.run // run.signals/properties/started/discovery are stable until Stop replaces run.
	svcLogger := scopedLogger(e.logger, svc.Name())
	svcEnvCtx := newEnvContext(run.properties, &e.mu, svcLogger)

	if err := e.waitForDependencies(pCtx, svc); err != nil {
		return err
	}

	discoveredProps, found, err := run.discovery.Discover(pCtx, svc)
	if err != nil {
		return fmt.Errorf("discovery failed for service %s: %w", svc.Name(), err)
	}
	if found {
		svcLogger.Info("♻️  Reusing service", "name", svc.Name())
		e.mu.Lock()
		for k, v := range discoveredProps {
			run.properties[k] = v
		}
		e.mu.Unlock()
		close(run.signals[svc.Name()])
		return nil
	}

	props, err := svc.Start(pCtx, svcEnvCtx)
	if err != nil {
		return fmt.Errorf("failed to start service %s: %w", svc.Name(), err)
	}

	e.mu.Lock()
	run.started[svc.Name()] = true
	for k, v := range props {
		run.properties[k] = v
	}
	e.mu.Unlock()

	if err := run.discovery.Publish(pCtx, svc, props.snapshot()); err != nil {
		return fmt.Errorf("failed to publish service %s: %w", svc.Name(), err)
	}

	close(run.signals[svc.Name()])
	return nil
}

// waitForDependencies blocks until every dependency's start signal is closed
// or the parent context is canceled. Validity of dependency names is
// guaranteed by dag.Validate in prepareStart, so a missing entry here is a
// programmer error (not user input).
func (e *Env) waitForDependencies(pCtx context.Context, svc Service) error {
	signals := e.run.signals
	for _, depName := range svc.Dependencies() {
		select {
		case <-signals[depName]:
		case <-pCtx.Done():
			return pCtx.Err()
		}
	}
	return nil
}

// runOnStartHooks invokes all registered LifecycleHook.OnStart callbacks with
// a stable property snapshot taken after all services started. The snapshot
// shields hooks from any later property mutations (notably, the reset in Stop).
func (e *Env) runOnStartHooks(ctx context.Context) error {
	e.mu.RLock()
	propSnapshot := e.run.properties.snapshot()
	e.mu.RUnlock()

	envCtx := newEnvContext(propSnapshot, &e.mu, e.logger)
	for _, hook := range e.hooks {
		if err := hook.OnStart(ctx, envCtx); err != nil {
			return fmt.Errorf("lifecycle hook OnStart failed: %w", err)
		}
	}
	return nil
}

// Stop tears down all services this env started, in reverse-dependency order,
// and invokes registered OnStop hooks. Idempotent: a second concurrent or
// sequential call after the first completes is a no-op. Reused services are
// not stopped (this env did not start them).
func (e *Env) Stop(ctx context.Context) error {
	run, ok := e.beginStop()
	if !ok {
		return nil
	}

	stopErr := e.stopServices(ctx, run)
	hookErr := e.runOnStopHooks(ctx, run)

	e.finishStop()
	return errors.Join(stopErr, hookErr)
}

// beginStop atomically transitions out of running/starting into stopping,
// returning the runState to tear down. Returns ok=false if the env is idle
// or another goroutine is already stopping it — making Stop idempotent under
// concurrent callers.
func (e *Env) beginStop() (*runState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != stateRunning && e.state != stateStarting {
		return nil, false
	}
	e.state = stateStopping
	return e.run, true
}

// finishStop releases the runState and returns to stateIdle. Must be called
// exactly once per beginStop.
func (e *Env) finishStop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.run = nil
	e.state = stateIdle
}

// stopServices fans out per-service stop goroutines, with each waiting for
// its dependents to stop first. Reused services are not stopped — this env
// did not start them, so they are filtered out entirely.
func (e *Env) stopServices(ctx context.Context, run *runState) error {
	toStop := make([]Service, 0, len(e.services))
	for _, s := range e.services {
		if run.started[s.Name()] {
			toStop = append(toStop, s)
		}
	}

	dependents := make(map[string][]string, len(toStop))
	stopSignals := make(map[string]chan struct{}, len(toStop))
	for _, s := range toStop {
		stopSignals[s.Name()] = make(chan struct{})
	}
	for _, s := range toStop {
		for _, dep := range s.Dependencies() {
			if _, ok := stopSignals[dep]; ok {
				dependents[dep] = append(dependents[dep], s.Name())
			}
		}
	}

	g, pCtx := errgroup.WithContext(ctx)
	for _, s := range toStop {
		svc := s
		g.Go(func() error {
			defer close(stopSignals[svc.Name()])
			for _, depName := range dependents[svc.Name()] {
				select {
				case <-stopSignals[depName]:
				case <-pCtx.Done():
					return pCtx.Err()
				}
			}
			if err := svc.Stop(pCtx); err != nil {
				return fmt.Errorf("failed to stop service %s: %w", svc.Name(), err)
			}
			if err := run.discovery.Unpublish(pCtx, svc); err != nil {
				return fmt.Errorf("failed to unpublish service %s: %w", svc.Name(), err)
			}
			return nil
		})
	}
	return g.Wait()
}

// runOnStopHooks invokes all OnStop callbacks against a stable property
// snapshot, joining any returned errors. Hooks always run, even if a previous
// hook failed, to give cleanup-style hooks a chance to run.
func (e *Env) runOnStopHooks(ctx context.Context, run *runState) error {
	if len(e.hooks) == 0 {
		return nil
	}
	e.mu.RLock()
	propSnapshot := run.properties.snapshot()
	e.mu.RUnlock()
	envCtx := newEnvContext(propSnapshot, &e.mu, e.logger)

	var errs []error
	for _, hook := range e.hooks {
		if err := hook.OnStop(ctx, envCtx); err != nil {
			errs = append(errs, fmt.Errorf("lifecycle hook OnStop failed: %w", err))
		}
	}
	return errors.Join(errs...)
}

// validateServiceNames returns an error if any two services share a Name,
// since Name is the addressable handle used in dependency edges and the
// runState signals map.
func validateServiceNames(services []Service) error {
	seen := make(map[string]bool, len(services))
	for _, s := range services {
		if seen[s.Name()] {
			return fmt.Errorf("duplicate service name: %s", s.Name())
		}
		seen[s.Name()] = true
	}
	return nil
}

// validateDependencies wraps the generic DAG check so error messages speak
// in service vocabulary rather than node vocabulary.
func validateDependencies(services []Service) error {
	nodes := make([]dag.Node, len(services))
	for i, s := range services {
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

// serviceNode adapts Service to dag.Node so dependency validation can run
// against the generic DAG package without it knowing about services.
type serviceNode struct {
	Service
}

func (s serviceNode) ID() string {
	return s.Name()
}
