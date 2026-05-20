package agent

import (
	"log"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/ory/fosite"
)

// handleOAuthToken is the /api/oauth/token endpoint. Thin wrapper
// over fosite's NewAccessRequest → NewAccessResponse → WriteAccess-
// Response chain. Everything interesting (grant_type dispatch, PKCE
// verification, refresh rotation with reuse defense, RFC 6749 §5.1
// no-store headers, error shape) lives in fosite; our job here is to
// adapt HTTP ↔ fosite and to inject the session type fosite hydrates
// into.
//
// 404s when the OAuth provider isn't wired (operator opted out via
// not calling SetOAuthProvider). Matches the discovery / DCR / etc.
// 404-when-not-configured pattern from the hand-rolled branch.
func (a *API) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if a.oauthProvider == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	// Hand fosite a fresh session pointer. NewAccessRequest will
	// populate it from the stored auth-code / refresh-token row;
	// the populated session ends up on the response too (e.g. for
	// JWT access tokens — not used here but harmless).
	session := &oauth.Session{}

	accessReq, err := a.oauthProvider.NewAccessRequest(ctx, r, session)
	if err != nil {
		logTokenError(accessReq, "new_access_request", err)
		// fosite writes the canonical RFC 6749 §5.2 JSON error body
		// here: {"error":"invalid_grant",...} with correct status
		// code and Cache-Control: no-store.
		a.oauthProvider.WriteAccessError(ctx, w, accessReq, err)
		return
	}

	accessResp, err := a.oauthProvider.NewAccessResponse(ctx, accessReq)
	if err != nil {
		logTokenError(accessReq, "new_access_response", err)
		a.oauthProvider.WriteAccessError(ctx, w, accessReq, err)
		return
	}

	a.oauthProvider.WriteAccessResponse(ctx, w, accessReq, accessResp)
}

// logTokenError emits a structured line for a failed /token exchange.
// Captures enough to spot patterns (repeated invalid_grant from one
// client, brute-force bad-PKCE attempts) without leaking anything
// sensitive — fosite's error message is the only operator-visible
// detail. fosite may hand us a nil requester or a partial one when
// the request failed during parsing; we don't panic on either.
func logTokenError(req fosite.AccessRequester, stage string, err error) {
	clientID := ""
	grantType := ""
	if req != nil {
		if c := req.GetClient(); c != nil {
			clientID = c.GetID()
		}
		grantType = req.GetRequestForm().Get("grant_type")
	}
	log.Printf("[oauth] /token %s error: client=%q grant=%q err=%v",
		stage, clientID, grantType, err)
}
