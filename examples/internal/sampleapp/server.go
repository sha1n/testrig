// Package sampleapp implements the HTTP server shared by the example apps.
// It is intentionally config-library-agnostic: New takes primitives so
// neither Viper- nor koanf-typed Config types leak into the shared code.
//
// The Server exposes two endpoints:
//
//	POST /save?key=<k>   — fire-and-forget fetchAndStore against remoteURL
//	GET  /lookup?key=<k> — return previously stored body, 404 if missing
package sampleapp

import (
	"database/sql"
	"net/http"
)

// Server bundles a database handle and a remote-service base URL and
// exposes an http.Handler with the demo routes.
type Server struct {
	db        *sql.DB
	remoteURL string
}

// New constructs a Server. remoteURL is the base URL of the remote service
// fetchAndStore calls (in the demo, the WireMock URL).
func New(db *sql.DB, remoteURL string) *Server {
	return &Server{db: db, remoteURL: remoteURL}
}

// Handler returns an http.Handler with all routes registered.
//
//	POST /save?key=<k>   — fire-and-forget fetchAndStore
//	GET  /lookup?key=<k> — return previously stored body, 404 if missing
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /save", s.handleSave)
	mux.HandleFunc("GET /lookup", s.handleLookup)
	return mux
}
