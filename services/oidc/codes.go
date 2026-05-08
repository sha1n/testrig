package oidc

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// codeTTL is the lifetime of an issued authorization code.
const codeTTL = 10 * time.Minute

// codeRecord captures the state stored against an issued code.
type codeRecord struct {
	clientID      string
	redirectURI   string
	scope         string
	state         string
	nonce         string
	audience      string // empty if /authorize was called without audience → id-token-only flow
	codeChallenge string
	subject       string
	expiresAt     time.Time
	consumed      bool
}

// codeStore is a single-use, expiry-bounded code store. Safe for concurrent
// use.
type codeStore struct {
	mu      sync.Mutex
	records map[string]*codeRecord
}

func newCodeStore() *codeStore {
	return &codeStore{records: make(map[string]*codeRecord)}
}

// issue returns a fresh random code (32-byte base64url) bound to rec.
func (s *codeStore) issue(rec *codeRecord) (string, error) {
	rec.expiresAt = time.Now().Add(codeTTL)
	rec.consumed = false
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[code] = rec
	return code, nil
}

// consume atomically marks the code consumed and returns its record. Returns
// (record, "ok"); or (nil, "not_found") / (nil, "expired") / (nil, "consumed").
// Caller decides which OAuth error to respond with based on the reason
// string. Always treats all three as `invalid_grant` per RFC 6749 §5.2.
func (s *codeStore) consume(code string) (*codeRecord, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[code]
	if !ok {
		return nil, "not_found"
	}
	if rec.consumed {
		return nil, "consumed"
	}
	if time.Now().After(rec.expiresAt) {
		delete(s.records, code)
		return nil, "expired"
	}
	rec.consumed = true
	delete(s.records, code)
	return rec, "ok"
}
