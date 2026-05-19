package agent

import (
	"net/http"
	"strings"
)

// OAuthAuthorizationServerMetadata is the RFC 8414 response shape for
// GET /.well-known/oauth-authorization-server. Field names use snake_case
// per the RFC. Only fields the e2a server actually advertises are listed
// — omitting an OPTIONAL field (e.g. introspection_endpoint, jwks_uri) is
// itself a signal to clients that the feature isn't supported.
type OAuthAuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

// handleOAuthDiscovery serves the RFC 8414 authorization-server metadata
// document so MCP clients (and any other OAuth 2.1 client) can locate the
// authorize/token/register/revoke endpoints from a single well-known URL.
//
// Unauthenticated by design — RFC 8414 §3 requires the document be
// publicly retrievable. Cache for 1h: values are stable for the lifetime
// of a deployment.
//
// 404 when OAuth isn't configured on this deployment (no oauthStore wired
// in via SetOAuthStore) or when http.public_url isn't set, since the
// metadata MUST contain absolute URLs and we'd rather hide the endpoint
// than emit values derived from request headers (X-Forwarded-Host
// spoofing → issuer confusion).
func (a *API) handleOAuthDiscovery(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil || a.publicURL == "" {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimRight(a.publicURL, "/")
	meta := OAuthAuthorizationServerMetadata{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/api/oauth/authorize",
		TokenEndpoint:                     base + "/api/oauth/token",
		RegistrationEndpoint:              base + "/api/oauth/register",
		RevocationEndpoint:                base + "/api/oauth/revoke",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_basic"},
		ScopesSupported:                   []string{"e2a"},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, meta)
}
