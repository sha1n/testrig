package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"time"
)

// rsaKeyBits is the RSA key size used for token signing. 2048 is the
// minimum recommended for RS256.
const rsaKeyBits = 2048

// startServer brings up the HTTP listener on 127.0.0.1:0, builds the mux,
// generates an RSA-2048 key, and stores everything on the Issuer. Caller
// holds i.mu.
func (i *Issuer) startServer(ctx context.Context) error {
	priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return fmt.Errorf("oidc: generate RSA key: %w", err)
	}
	i.privKey = priv
	i.codeStore = newCodeStore(i.codeTTL)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		i.privKey = nil // revert partial state so retries get a clean slate
		return fmt.Errorf("oidc: bind listener: %w", err)
	}
	i.listener = ln
	i.baseURL = "http://" + ln.Addr().String()

	mux := http.NewServeMux()
	// Routes added by discovery/jwks/authorize/token handler files as tasks land.
	i.registerRoutes(mux)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	i.server = srv

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// stopServer shuts the HTTP server and clears runtime state. Caller holds i.mu.
func (i *Issuer) stopServer(ctx context.Context) error {
	if i.server == nil {
		return nil
	}
	srv := i.server
	i.server = nil
	i.listener = nil
	i.baseURL = ""
	i.privKey = nil
	i.codeStore = nil
	return srv.Shutdown(ctx)
}

// registerRoutes wires endpoint handlers onto the provided mux. Handlers
// are added incrementally by Tasks 4, 5, 8, and 10.
func (i *Issuer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/openid-configuration", i.handleDiscovery)
	mux.HandleFunc("GET /.well-known/jwks.json", i.handleJWKS)
	mux.HandleFunc("GET /authorize", i.handleAuthorize)
	mux.HandleFunc("POST /authorize", i.handleAuthorize)
	mux.HandleFunc("POST /token", i.handleToken)
}

// generateRandomHex returns n random bytes as a lowercase hex string.
// Used for default ClientID and KeyID.
func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidc: random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
