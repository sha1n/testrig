// Package server is the koanf-app's HTTP server. Layout mirrors the
// viper-app: server.go owns wiring; handlers.go owns request/response
// logic; store.go owns DB access.
package server

import (
	"database/sql"
	"net/http"

	"github.com/sha1n/testrig/examples/koanf-app/config"
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
