package testrig

import "context"

// Service represents a stateful dependency managed by an Env.
//
// A Service must be safe to call from at most one Env's lifecycle at a time:
// Env.Start invokes its Start once, and Env.Stop invokes its Stop once. The
// same Service instance may be passed to multiple Envs, but the Env will
// only call Stop on services it actually started (not on those reused via
// discovery).
type Service interface {
	// Name returns the per-env display name of this service. It is used as
	// the lookup key for declared Dependencies and as the property prefix
	// for well-known address keys (Name+".host", Name+".port").
	Name() string

	// Identifier returns a stable content-addressed ID over the service
	// configuration. Two service instances with the same Identifier are
	// considered equivalent for cross-process and cross-env reuse via
	// discovery; it must NOT depend on per-instance runtime state.
	Identifier() string

	// Dependencies returns the Names of services this service requires.
	// Cycles or references to unknown services cause Env.Start to fail
	// before any service starts.
	Dependencies() []string

	// Start brings the service up and returns the properties it publishes
	// (typically host/port/credentials). It receives an EnvContext through
	// which it can read properties published by its dependencies.
	Start(ctx context.Context, envCtx EnvContext) (Properties, error)

	// Stop tears the service down. It is invoked by Env.Stop in
	// reverse-dependency order. Implementations should be idempotent so
	// repeated Stop calls (e.g. user error or rollback paths) are safe.
	Stop(ctx context.Context) error
}
