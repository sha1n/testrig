package sampleapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
)

// handleSave queues an asynchronous fetchAndStore for the given key and
// returns 202 Accepted. The work outlives the request via a fresh ctx;
// tests poll the DB to observe completion.
//
// Precondition: authMiddleware has populated the subject in r.Context().
// Both routes are wired through that middleware in Handler(), so the
// subject is always set when this handler runs.
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	sub, _ := SubjectFromContext(r.Context())
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	go func() {
		if err := s.fetchAndStore(context.Background(), sub, key); err != nil {
			log.Printf("fetchAndStore user=%s key=%s: %v", sub, key, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// handleLookup returns the stored body for (sub, key), or 404 if nothing is
// stored for this user under that key.
//
// Precondition: see handleSave.
func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	sub, _ := SubjectFromContext(r.Context())
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	body, err := s.lookup(r.Context(), sub, key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// fetchAndStore looks up `key` against the configured remote service and
// persists the response body into the DB scoped to (userID, key).
func (s *Server) fetchAndStore(ctx context.Context, userID, key string) error {
	target := s.remoteURL + "/lookup?key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote GET %s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read remote body: %w", err)
	}
	return s.store(ctx, userID, key, string(body))
}
