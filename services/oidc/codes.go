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

// consume atomically validates and removes the record bound to code. The
// record is removed (single-use) only when all checks pass. expectedRedirectURI
// is matched against the redirect_uri the code was issued with — a mismatch
// leaves the record intact so a legitimate retry with the correct value can
// still succeed (per RFC 6749 §4.1.3 validation ordering).
//
// Returns (record, "ok") on success; (nil, reason) otherwise where reason is
// "not_found", "expired", or "redirect_uri_mismatch". The caller maps all
// non-"ok" reasons to `invalid_grant` per RFC 6749 §5.2.
func (s *codeStore) consume(code, expectedRedirectURI string) (*codeRecord, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[code]
	if !ok {
		return nil, "not_found"
	}
	if time.Now().After(rec.expiresAt) {
		delete(s.records, code)
		return nil, "expired"
	}
	if rec.redirectURI != expectedRedirectURI {
		// Leave the record in place: a legitimate retry with the correct
		// redirect_uri must still succeed.
		return nil, "redirect_uri_mismatch"
	}
	delete(s.records, code)
	return rec, "ok"
}
