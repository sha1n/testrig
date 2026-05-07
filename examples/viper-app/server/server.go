// Package server is the viper-app's HTTP server. It defines the Server
// type, route registration, and pure handler entry points; handler
// bodies live in handlers.go and DB access in store.go.
package server

import (
	"database/sql"
	"net/http"

	"github.com/sha1n/testrig/examples/viper-app/config"
)

// Server bundles configuration and a database handle and exposes an
// http.Handler with the demo routes.
type Server struct {
	cfg *config.Config
	db  *sql.DB
}

// New constructs a Server.
func New(cfg *config.Config, db *sql.DB) *Server {
	return &Server{cfg: cfg, db: db}
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
