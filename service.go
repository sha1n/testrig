package testrig

import "context"

// Service represents a stateful dependency managed by an Env.
//
// Env.Start invokes Start once. Env.Stop invokes Stop once. A Service is owned
// by the Env it was added to; passing the same instance to multiple Envs is a
// programmer error.
type Service interface {
	// Name returns the per-env display name of this service. It is used as
	// the lookup key for declared Dependencies and as a convenient property
	// prefix (e.g. Name+".host"). Names must be unique within an Env.
	Name() string

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
