package testrig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Env manages a set of Service implementations, handling their lifecycle
// and property aggregation.
//
// Construct via New(name) and configure via the chainable With* methods.
// All configuration is applied at construction time; mutating an Env
// after Start (or concurrently with Start) is a programmer error.
type Env struct {
	// Set at construction; mutated by fluent builder methods only before
	// Start is invoked.
	name   string
	logger *slog.Logger
	hooks  []LifecycleHook
	tracks []*Stages

	// Mutable lifecycle state, guarded by mu.
	mu         sync.RWMutex
	state      envState
	properties Properties    // non-nil iff state != stateIdle
	started    [][][]Service // started[trackIdx][stageIdx] = services that successfully started in that stage
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

// New creates a new Env with the given name and isolation-safe defaults
// (slog.Default() logger, no services, no hooks). Configure further via
// the chainable With* methods.
func New(name string) *Env {
	return &Env{
		name:   name,
		logger: slog.Default(),
		state:  stateIdle,
	}
}

// With appends a single-stage track containing the given services. The
// services in this track will start concurrently with each other and with
// any other tracks already registered. Multiple With calls accumulate as
// distinct tracks. Panics if any service is nil.
func (e *Env) With(services ...Service) *Env {
	for i, s := range services {
		if s == nil {
			panic(fmt.Sprintf("testrig: With received a nil Service at index %d", i))
		}
	}
	e.tracks = append(e.tracks, singleStage(services))
	return e
}

// WithStages appends a multi-stage track to the env. The track will run
// concurrently with all other registered tracks; within the track, the
// stages execute in the order declared. Panics if s is nil.
func (e *Env) WithStages(s *Stages) *Env {
	if s == nil {
		panic("testrig: WithStages requires a non-nil *Stages")
	}
	e.tracks = append(e.tracks, s)
	return e
}

// WithLogger replaces the env's logger. Panics if logger is nil.
func (e *Env) WithLogger(logger *slog.Logger) *Env {
	if logger == nil {
		panic("testrig: WithLogger requires a non-nil *slog.Logger")
	}
	e.logger = logger
	return e
}

// WithLifecycleHooks appends lifecycle hooks (accumulative across calls).
// Panics if any hook is nil.
func (e *Env) WithLifecycleHooks(hooks ...LifecycleHook) *Env {
	for i, h := range hooks {
		if h == nil {
			panic(fmt.Sprintf("testrig: WithLifecycleHooks received a nil LifecycleHook at index %d", i))
		}
	}
	e.hooks = append(e.hooks, hooks...)
	return e
}

// Name returns the environment's display name as set in New.
func (e *Env) Name() string {
	return e.name
}

// Properties returns a snapshot of all properties in the environment, or
// an empty map when the env is idle. The same data is returned directly
// by Start; Properties is for callers that only hold an *Env (lifecycle
// hooks internally, code paths that didn't capture Start's return).
func (e *Env) Properties() Properties {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.properties == nil {
		return make(Properties)
	}
	return e.properties.snapshot()
}

// Start brings every registered track up. Tracks run concurrently with
// each other; within a track, stages run sequentially. AfterStart hooks
// run once every service has started successfully and the env has
// transitioned to running. On any error — service Start failure, context
// cancellation, or hook failure — Start aborts and triggers rollback via
// Stop, returning the original error joined with any rollback error.
//
// Start may not be called on a running or starting env. After a
// successful Stop, the same Env can be Started again with fresh state.
//
// On success, Start returns a snapshot of every property published by
// every service that started. The returned snapshot is the same value
// passed to AfterStart hooks: caller and hooks observe identical state.
// The map is owned by the caller; mutating it has no effect on the env.
// On any failure path the first return value is nil.
func (e *Env) Start(ctx context.Context) (Properties, error) {
	if err := e.beginStart(); err != nil {
		return nil, err
	}

	g, gctx := errgroup.WithContext(ctx)
	for trackIdx, track := range e.tracks {
		trackIdx := trackIdx
		track := track
		g.Go(func() error { return e.runTrack(gctx, trackIdx, track) })
	}
	if err := g.Wait(); err != nil {
		return nil, errors.Join(err, e.Stop(context.Background()))
	}

	e.mu.Lock()
	e.state = stateRunning
	snapshot := e.properties.snapshot()
	e.mu.Unlock()

	if err := e.runAfterStartHooks(ctx, snapshot); err != nil {
		return nil, errors.Join(err, e.Stop(context.Background()))
	}
	return snapshot, nil
}

// runTrack runs the stages of a single track in order. Each stage's
// services start concurrently. Aborts on the first stage failure.
func (e *Env) runTrack(ctx context.Context, trackIdx int, track *Stages) error {
	for stageIdx, stage := range track.stages {
		sg, sgctx := errgroup.WithContext(ctx)
		for _, svc := range stage {
			svc := svc
			sg.Go(func() error {
				handle := &envHandle{env: e, name: svc.Name()}
				props, err := svc.Start(sgctx, handle)
				if err != nil {
					return fmt.Errorf("failed to start service %s: %w", svc.Name(), err)
				}
				e.mu.Lock()
				for k, v := range props {
					e.properties[k] = v
				}
				e.recordStarted(trackIdx, stageIdx, svc)
				e.mu.Unlock()
				return nil
			})
		}
		if err := sg.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// recordStarted appends svc to e.started[trackIdx][stageIdx]. The outer
// and middle dimensions are pre-allocated in beginStart to match the
// shape of e.tracks. Caller must hold e.mu.
func (e *Env) recordStarted(trackIdx, stageIdx int, svc Service) {
	e.started[trackIdx][stageIdx] = append(e.started[trackIdx][stageIdx], svc)
}

// beginStart transitions the env to stateStarting under lock, validates
// service names, and allocates fresh per-run state. Returns an error
// (without changing state) if the env is not idle or if service names
// are duplicated across tracks.
func (e *Env) beginStart() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != stateIdle {
		return fmt.Errorf("environment %s is %s, must be idle to start", e.name, e.state)
	}
	if err := validateServiceNames(e.tracks); err != nil {
		return err
	}
	e.properties = make(Properties)
	e.started = make([][][]Service, len(e.tracks))
	for i, t := range e.tracks {
		e.started[i] = make([][]Service, len(t.stages))
	}
	e.state = stateStarting
	return nil
}

// Stop tears down all services that successfully started. Tracks stop
// concurrently with each other; within a track, stages stop in reverse
// order (last stage first). Idempotent: a second concurrent or sequential
// call after the first completes is a no-op.
func (e *Env) Stop(ctx context.Context) error {
	started, ok := e.beginStop()
	if !ok {
		return nil
	}

	stopErr := e.stopTracks(ctx, started)
	hookErr := e.runAfterStopHooks(ctx)

	e.finishStop()
	return errors.Join(stopErr, hookErr)
}

// beginStop atomically transitions out of running/starting into stopping,
// returning the started-services structure to tear down. Returns
// ok=false if the env is idle or another goroutine is already stopping
// it — making Stop idempotent under concurrent callers.
func (e *Env) beginStop() ([][][]Service, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != stateRunning && e.state != stateStarting {
		return nil, false
	}
	e.state = stateStopping
	// Deep copy so the running goroutines and the stopper don't alias.
	started := make([][][]Service, len(e.started))
	for i, track := range e.started {
		started[i] = make([][]Service, len(track))
		for j, stage := range track {
			started[i][j] = append([]Service(nil), stage...)
		}
	}
	return started, true
}

// stopTracks stops each track concurrently. Within a track, stages run
// in reverse order; within a stage, services stop in parallel.
func (e *Env) stopTracks(ctx context.Context, started [][][]Service) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, trackStarted := range started {
		ts := trackStarted
		g.Go(func() error { return e.stopTrack(gctx, ts) })
	}
	return g.Wait()
}

// stopTrack stops one track's services in reverse-stage order. Within a
// stage, services stop concurrently. Errors from all stages are joined.
func (e *Env) stopTrack(ctx context.Context, trackStarted [][]Service) error {
	var errs []error
	for i := len(trackStarted) - 1; i >= 0; i-- {
		stage := trackStarted[i]
		sg, sgctx := errgroup.WithContext(ctx)
		for _, svc := range stage {
			svc := svc
			sg.Go(func() error {
				if err := svc.Stop(sgctx); err != nil {
					return fmt.Errorf("failed to stop service %s: %w", svc.Name(), err)
				}
				return nil
			})
		}
		if err := sg.Wait(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// finishStop releases per-run state and returns to stateIdle. Must be
// called exactly once per beginStop.
func (e *Env) finishStop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.properties = nil
	e.started = nil
	e.state = stateIdle
}

// runAfterStartHooks invokes all AfterStart callbacks against the
// caller-supplied property snapshot. The snapshot is shared with the
// value returned by Start, so hooks and the caller observe identical
// state. Returns the first hook error.
func (e *Env) runAfterStartHooks(ctx context.Context, snapshot Properties) error {
	if len(e.hooks) == 0 {
		return nil
	}
	for _, hook := range e.hooks {
		if err := hook.AfterStart(ctx, snapshot, e.logger); err != nil {
			return fmt.Errorf("lifecycle hook AfterStart failed: %w", err)
		}
	}
	return nil
}

// runAfterStopHooks invokes all AfterStop callbacks against a stable
// property snapshot, joining any returned errors. Hooks always run, even
// if a previous hook failed, to give cleanup-style hooks a chance to run.
func (e *Env) runAfterStopHooks(ctx context.Context) error {
	if len(e.hooks) == 0 {
		return nil
	}
	e.mu.RLock()
	propSnapshot := e.properties.snapshot()
	e.mu.RUnlock()

	var errs []error
	for _, hook := range e.hooks {
		if err := hook.AfterStop(ctx, propSnapshot, e.logger); err != nil {
			errs = append(errs, fmt.Errorf("lifecycle hook AfterStop failed: %w", err))
		}
	}
	return errors.Join(errs...)
}

// scopedLogger returns a child logger with the given service name
// attribute already attached.
func scopedLogger(parent *slog.Logger, name string) *slog.Logger {
	return parent.With("service", name)
}

// validateServiceNames returns an error if any two services across all
// tracks share a Name.
func validateServiceNames(tracks []*Stages) error {
	seen := make(map[string]bool)
	for _, t := range tracks {
		for _, stage := range t.stages {
			for _, s := range stage {
				if seen[s.Name()] {
					return fmt.Errorf("duplicate service name: %s", s.Name())
				}
				seen[s.Name()] = true
			}
		}
	}
	return nil
}
