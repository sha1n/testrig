package oidc

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signClaims signs the provided MapClaims with the issuer's RSA key,
// stamping the kid header. Returns "issuer not started" if the keypair has
// not been generated yet.
func (i *Issuer) signClaims(claims jwt.MapClaims) (string, error) {
	if i.privKey == nil {
		return "", errors.New("oidc: issuer not started")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = i.keyID
	signed, err := tok.SignedString(i.privKey)
	if err != nil {
		return "", fmt.Errorf("oidc: sign: %w", err)
	}
	return signed, nil
}

// sign auto-fills iss and iat in claims only when absent, then signs.
func (i *Issuer) sign(claims jwt.MapClaims) (string, error) {
	if i.privKey == nil {
		return "", errors.New("oidc: issuer not started")
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = i.IssuerURL()
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = time.Now().Unix()
	}
	return i.signClaims(claims)
}

// signFor signs a token with subject, audience, and ttl. Validates inputs.
func (i *Issuer) signFor(subject, audience string, ttl time.Duration) (string, error) {
	if i.privKey == nil {
		return "", errors.New("oidc: issuer not started")
	}
	if subject == "" {
		return "", errors.New("oidc: subject must not be empty")
	}
	if audience == "" {
		return "", errors.New("oidc: audience must not be empty")
	}
	if ttl <= 0 {
		return "", errors.New("oidc: ttl must be > 0")
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": i.IssuerURL(),
		"sub": subject,
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	return i.signClaims(claims)
}
