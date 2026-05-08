package oidc

import (
	"encoding/json"
	"net/http"
)

type discoveryDoc struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
	SubjectTypesSupported             []string `json:"subject_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

// handleDiscovery serves the OIDC discovery document. Always 200; the body
// reflects the post-Start configuration.
func (i *Issuer) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := discoveryDoc{
		Issuer:                            i.IssuerURL(),
		AuthorizationEndpoint:             i.AuthorizationURL(),
		TokenEndpoint:                     i.TokenURL(),
		JWKSURI:                           i.JWKSURL(),
		UserinfoEndpoint:                  i.UserinfoURL(),
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "client_credentials", "refresh_token"},
		IDTokenSigningAlgValuesSupported:  []string{"RS256"},
		SubjectTypesSupported:             []string{"public"},
		TokenEndpointAuthMethodsSupported: []string{"client_secret_basic", "client_secret_post"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}
