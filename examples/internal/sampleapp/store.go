package sampleapp

import (
	"context"
	"fmt"
)

// store inserts (or upserts) a response row scoped to (userID, key).
func (s *Server) store(ctx context.Context, userID, key, body string) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO responses (user_id, key, response) VALUES ($1, $2, $3)
        ON CONFLICT (user_id, key) DO UPDATE SET response = EXCLUDED.response, created_at = now()
    `, userID, key, body)
	if err != nil {
		return fmt.Errorf("insert response user=%s key=%s: %w", userID, key, err)
	}
	return nil
}

// lookup returns the stored response body for (userID, key). Returns
// sql.ErrNoRows if no row exists in this user's namespace.
func (s *Server) lookup(ctx context.Context, userID, key string) (string, error) {
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT response FROM responses WHERE user_id = $1 AND key = $2`,
		userID, key).Scan(&body)
	return body, err
}
