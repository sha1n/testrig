package api

import (
	"log/slog"
)

// EnvHandle is the read access surface a Service receives in its Start
// method. It exposes env-scoped state: the env name, a service-scoped
// logger, and a snapshot of properties accumulated by previously-completed
// stages.
//
// Properties() returns a snapshot taken at call time. Properties published
// by services in earlier stages of the same track are guaranteed to be
// visible; properties from services running concurrently in the same
// stage are not.
type EnvHandle interface {
	// Name returns the env's display name as set in New.
	Name() string

	// Logger returns a logger scoped to the calling service (with a
	// `service=<name>` attribute already attached).
	Logger() *slog.Logger

	// Properties returns a snapshot of the env's properties at this
	// moment. The returned map is owned by the caller; mutating it has
	// no effect on the env.
	Properties() Properties
}

// StubEnvHandle returns a fixed EnvHandle for use in tests of Service
// implementations. The properties map is snapshotted at construction
// time, so subsequent mutations to the caller's map have no effect on
// the stub. A nil logger falls back to slog.Default(); a nil properties
// map yields an empty Properties from Properties().
func StubEnvHandle(name string, logger *slog.Logger, props Properties) EnvHandle {
	if logger == nil {
		logger = slog.Default()
	}
	return &stubEnvHandle{name: name, logger: logger, props: props.Snapshot()}
}

type stubEnvHandle struct {
	name   string
	logger *slog.Logger
	props  Properties
}

func (s *stubEnvHandle) Name() string           { return s.name }
func (s *stubEnvHandle) Logger() *slog.Logger   { return s.logger }
func (s *stubEnvHandle) Properties() Properties { return s.props.Snapshot() }
