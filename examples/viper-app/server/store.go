package server

import (
	"context"
	"fmt"
)

// store inserts (or upserts) a response row.
func (s *Server) store(ctx context.Context, key, body string) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO responses (key, response) VALUES ($1, $2)
        ON CONFLICT (key) DO UPDATE SET response = EXCLUDED.response, created_at = now()
    `, key, body)
	if err != nil {
		return fmt.Errorf("insert response key=%s: %w", key, err)
	}
	return nil
}

// lookup returns the stored response body for the given key. Returns
// sql.ErrNoRows if the key is not present.
func (s *Server) lookup(ctx context.Context, key string) (string, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT response FROM responses WHERE key = $1`, key).Scan(&body)
	return body, err
}
