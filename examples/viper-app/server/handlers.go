package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
)

// handleSave queues an asynchronous fetchAndStore for the given key and
// returns 202 Accepted. The work outlives the request via a fresh ctx;
// tests poll the DB to observe completion.
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	go func() {
		if err := s.fetchAndStore(context.Background(), key); err != nil {
			log.Printf("fetchAndStore key=%s: %v", key, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// handleLookup returns the stored body for the given key, or 404 if
// nothing is stored.
func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	body, err := s.lookup(r.Context(), key)
	switch {
	case err == sql.ErrNoRows:
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// fetchAndStore looks up `key` against the configured remote service and
// persists the response body into the DB under that key. Used as the
// async worker behind POST /save.
func (s *Server) fetchAndStore(ctx context.Context, key string) error {
	target := s.cfg.RemoteURL + "/lookup?key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read remote body: %w", err)
	}
	return s.store(ctx, key, string(body))
}
