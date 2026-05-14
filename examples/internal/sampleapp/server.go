// Package sampleapp implements the HTTP server shared by the example apps.
// It is intentionally config-library-agnostic: New takes primitives and a
// small Config so neither Viper- nor koanf-typed Config types leak into the
// shared code.
//
// The Server exposes two protected endpoints, both validated by a Bearer-JWT
// middleware against the AuthConfig supplied at construction time:
//
//	POST /save?key=<k>   — fire-and-forget fetchAndStore against remoteURL,
//	                       persisting the response under (sub, key)
//	GET  /lookup?key=<k> — return previously stored body for (sub, key),
//	                       404 if missing
package sampleapp

import (
	"database/sql"
	"net/http"
)

// Config is the runtime configuration sampleapp.New accepts. RemoteURL is the
// upstream service the /save handler calls; Auth is how incoming JWTs are
// validated. See AuthConfig.
type Config struct {
	RemoteURL string
	Auth      AuthConfig
}

// Server bundles a database handle, remote-service base URL, and auth config,
// and exposes an http.Handler with the demo routes.
type Server struct {
	db        *sql.DB
	remoteURL string
	auth      AuthConfig
}

// New constructs a Server.
func New(db *sql.DB, cfg Config) *Server {
	return &Server{db: db, remoteURL: cfg.RemoteURL, auth: cfg.Auth}
}

// Handler returns an http.Handler with all routes registered. Both routes are
// wrapped in the Bearer-JWT middleware built from the Server's AuthConfig.
//
//	POST /save?key=<k>   — fire-and-forget fetchAndStore, scoped to JWT sub
//	GET  /lookup?key=<k> — return previously stored body, scoped to JWT sub
func (s *Server) Handler() http.Handler {
	authn := authMiddleware(s.auth)
	mux := http.NewServeMux()
	mux.Handle("POST /save", authn(http.HandlerFunc(s.handleSave)))
	mux.Handle("GET /lookup", authn(http.HandlerFunc(s.handleLookup)))
	return mux
}
