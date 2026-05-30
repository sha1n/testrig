package api

import (
	"context"
	"log/slog"
)

// LifecycleHook observes successful Env start and shutdown. Hooks attached
// via Env.WithLifecycleHooks are invoked in registration order.
//
// Typical uses: setting OS environment variables for the test process,
// writing transient config files, registering cleanup tasks. For most
// configuration plumbing, prefer reading env.Properties() directly into
// your config library — hooks are a convenience, not a requirement.
type LifecycleHook interface {
	// AfterStart is called once every service in the environment has
	// started successfully and the env has transitioned to the running
	// state. It is part of the Start sequence: if it returns an error,
	// Env.Start fails and triggers a full rollback (Stop is invoked).
	//
	// `props` is a stable snapshot of the environment's properties at
	// the moment AfterStart fires; it is safe to read concurrently.
	// Hooks must not mutate `props` — the same map is returned to the
	// caller of Env.Start, and is shared with every other AfterStart
	// hook in registration order.
	AfterStart(ctx context.Context, props Properties, logger *slog.Logger) error

	// AfterStop is called once every service in the environment has
	// stopped, as part of the Stop sequence. All registered hooks run
	// even if a previous hook returned an error, so cleanup-style hooks
	// always get a chance to execute. Returned errors are joined into
	// the error returned by Env.Stop.
	AfterStop(ctx context.Context, props Properties, logger *slog.Logger) error
}
