package api

import "context"

// Service represents a stateful dependency managed by an Env.
//
// Env.Start invokes Start once per service, concurrently. Env.Stop invokes
// Stop once per service, concurrently, and only on services whose Start
// returned without error. A Service is owned by the Env it was added to;
// passing the same instance to multiple Envs is a programmer error.
type Service interface {
	// Name returns the per-env display name of this service. Names must be
	// unique within an Env. Used in logs and as a convenient property
	// prefix (e.g. Name+".host") in pre-built services.
	Name() string

	// Start brings the service up and returns the properties it publishes
	// (typically host/port/credentials/URL). It receives an EnvHandle that
	// exposes the env's name, a service-scoped logger, and a snapshot of
	// properties published by services in previously-completed stages.
	//
	// Start is invoked concurrently with sibling services in the same
	// stage and cannot rely on observing their published properties.
	// Properties published by services in earlier stages of the same
	// track are visible via env.Properties().
	Start(ctx context.Context, env EnvHandle) (Properties, error)

	// Stop tears the service down. Implementations should be idempotent
	// so repeated Stop calls (e.g. user error or rollback paths) are safe.
	Stop(ctx context.Context) error
}
