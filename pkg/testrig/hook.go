package testrig

import "context"

// LifecycleHook observes successful Env start and shutdown. Hooks attached
// via WithHooks are invoked in registration order after all services have
// finished starting (OnStart) and after all services have stopped (OnStop).
//
// Typical uses: setting OS environment variables for the test process,
// writing transient config files, registering cleanup tasks. For most
// configuration plumbing, prefer reading env.Properties() directly into
// your config library — hooks are a convenience, not a requirement.
type LifecycleHook interface {
	// OnStart is called after all services in the environment have started
	// successfully. If it returns an error, Env.Start fails and triggers a
	// rollback (Stop is invoked).
	OnStart(ctx context.Context, envCtx EnvContext) error

	// OnStop is called after all services in the environment have stopped.
	// All registered hooks run even if a previous hook returned an error,
	// so cleanup-style hooks always get a chance. Returned errors are
	// joined into the error returned by Env.Stop.
	OnStop(ctx context.Context, envCtx EnvContext) error
}
