package oidc

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
)

type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// handleJWKS serves the JWKS document containing the issuer's RSA public
// key. Always 200; the kid matches the configured KeyID.
func (i *Issuer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	pub := i.PublicKey()
	if pub == nil {
		http.Error(w, "issuer not started", http.StatusInternalServerError)
		return
	}
	doc := jwksDoc{Keys: []jwk{{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: i.keyID,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}
