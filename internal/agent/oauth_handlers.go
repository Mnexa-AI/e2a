package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/jackc/pgx/v5"
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

// ───────────────────────── /authorize ─────────────────────────

// handleOAuthAuthorize is the entry point for the OAuth browser flow.
//
// Steps:
//  1. Hand the request to fosite to validate every parameter (client
//     exists, redirect_uri matches the registered set, response_type
//     == "code", PKCE shape, scope, state). fosite writes the
//     appropriate RFC 6749 §4.1.2.1 error response itself on failure
//     — either a redirect to redirect_uri?error=… (when the URI was
//     verified-safe) or a direct 400 (when it wasn't).
//  2. Check the user's session cookie. No session → 302 to
//     /api/auth/login. Today we don't carry a return_to (port lands
//     in a later slice); operators see a log line on every such
//     redirect so the missing piece is visible.
//  3. With a session, 302 to {publicURL}/oauth/consent?<params>. The
//     consent UI in web/ POSTs back to /api/oauth/consent.
func (a *API) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if a.oauthProvider == nil {
		http.NotFound(w, r)
		return
	}
	if a.publicURL == "" {
		http.Error(w, "OAuth flow not configured: http.public_url is unset", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()

	ar, err := a.oauthProvider.NewAuthorizeRequest(ctx, r)
	if err != nil {
		// fosite writes the right response shape (redirect-with-error
		// when the URI was verified, direct error otherwise).
		a.oauthProvider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	// Session check. The userAuth path produces the same session
	// cookie the rest of the dashboard uses (e2a_session).
	if a.userAuth == nil {
		http.Error(w, "user auth not configured on this deployment", http.StatusServiceUnavailable)
		return
	}
	if user := a.userAuth.AuthenticateRequest(r); user == nil {
		// TODO: thread r.URL.RequestURI() through HandleLogin's
		// return_to once that exists on this branch. Today the user
		// lands on /dashboard and re-triggers from their MCP client.
		log.Printf("[oauth] /authorize no session; redirecting to login (return_to not yet wired)")
		http.Redirect(w, r, strings.TrimRight(a.publicURL, "/")+"/api/auth/login", http.StatusFound)
		return
	}

	// 302 to consent UI with all authorize params re-passed. The
	// consent page hidden-fields these back into its POST so we can
	// re-parse the request via fosite without trusting a server-side
	// session stash (which would otherwise be the natural place but
	// adds operational complexity for little gain).
	consentURL, _ := url.Parse(strings.TrimRight(a.publicURL, "/") + "/oauth/consent")
	consentURL.RawQuery = r.URL.RawQuery
	http.Redirect(w, r, consentURL.String(), http.StatusFound)
}

// ───────────────────────── /consent ─────────────────────────

// handleOAuthConsent processes the consent form POSTed by the web/
// consent UI. Form fields:
//
//   - all the authorize-request params (response_type, client_id,
//     redirect_uri, scope, state, code_challenge,
//     code_challenge_method) — re-passed as hidden inputs by the
//     consent page so we can rebuild the fosite AuthorizeRequester
//   - action: "allow" | "deny"
//   - agent_choice: "create_new" | "existing:<email>"
//   - new_agent_slug: optional, used when agent_choice == create_new
//
// On allow + create_new we open a transaction (via Storage.Pool().Begin)
// that spans BOTH the agent insert (identity package) AND the auth-
// code insert (oauth package — fosite calls our Storage internally).
// The same context carries the tx so both packages join it; commit
// happens after both succeed. A partial failure rolls back, so we
// can't leak an agent the user never authorized.
func (a *API) handleOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if a.oauthProvider == nil {
		http.NotFound(w, r)
		return
	}
	if a.userAuth == nil {
		http.Error(w, "user auth not configured on this deployment", http.StatusServiceUnavailable)
		return
	}
	if a.oauthStorage == nil {
		http.Error(w, "oauth storage not configured on this deployment", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse form body", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	// Rebuild the authorize request from the form values. fosite's
	// NewAuthorizeRequest reads from r.URL.Query() — we synthesize a
	// query-string by promoting the POSTed form fields. This way the
	// same validator runs in the same configuration on both /authorize
	// and /consent, so a tampered hidden field gets caught.
	authReq := r.Clone(ctx)
	authReq.URL.RawQuery = r.PostForm.Encode()
	ar, err := a.oauthProvider.NewAuthorizeRequest(ctx, authReq)
	if err != nil {
		a.oauthProvider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	user := a.userAuth.AuthenticateRequest(r)
	if user == nil {
		http.Error(w, "session required: log in before consenting", http.StatusUnauthorized)
		return
	}

	action := r.PostFormValue("action")
	if action == "deny" {
		// RFC 6749 §4.1.2.1: redirect back with error=access_denied.
		// fosite's WriteAuthorizeError emits the redirect for us when
		// the error is shaped right.
		a.oauthProvider.WriteAuthorizeError(ctx, w, ar,
			fosite.ErrAccessDenied.WithHint("user denied consent"))
		return
	}
	if action != "allow" {
		http.Error(w, "action must be 'allow' or 'deny'", http.StatusBadRequest)
		return
	}

	// Resolve which agent the OAuth grant pins to. Either an existing
	// inbox the user already owns, or a freshly auto-created one on
	// the shared domain.
	agentChoice := r.PostFormValue("agent_choice")
	switch {
	case strings.HasPrefix(agentChoice, "existing:"):
		email := strings.TrimPrefix(agentChoice, "existing:")
		agent, err := a.store.GetAgentByEmail(ctx, email)
		if err != nil {
			http.Error(w, "chosen agent does not exist", http.StatusBadRequest)
			return
		}
		if agent.UserID != user.ID {
			http.Error(w, "you do not own that agent", http.StatusForbidden)
			return
		}
		// No agent creation needed — drop straight into the code-
		// issue path with the resolved email on the session.
		if err := a.issueOAuthCode(ctx, w, r, ar, user.ID, email); err != nil {
			log.Printf("[oauth] /consent issue (existing agent) failed: %v", err)
			a.oauthProvider.WriteAuthorizeError(ctx, w, ar, err)
		}

	case agentChoice == "create_new":
		if a.sharedDomain == "" {
			http.Error(w, "shared-domain auto-create is not configured", http.StatusServiceUnavailable)
			return
		}
		slug := strings.TrimSpace(r.PostFormValue("new_agent_slug"))
		if slug == "" {
			slug = defaultAgentSlug(ar.GetClient().GetID())
		}
		if err := validateSlug(slug); err != nil {
			http.Error(w, "invalid slug: "+err.Error(), http.StatusBadRequest)
			return
		}
		agentEmail := slug + "@" + a.sharedDomain
		if err := a.issueOAuthCodeWithNewAgent(ctx, w, r, ar, user.ID, agentEmail); err != nil {
			log.Printf("[oauth] /consent issue (new agent) failed: %v", err)
			a.oauthProvider.WriteAuthorizeError(ctx, w, ar, err)
		}

	default:
		http.Error(w, "agent_choice must be 'existing:<email>' or 'create_new'", http.StatusBadRequest)
	}
}

// issueOAuthCode is the no-new-agent path. fosite mints the code via
// our Storage on the pool (no cross-package tx needed). After fosite's
// NewAuthorizeResponse we write the redirect ourselves so we can
// append the RFC 9207 iss parameter — fosite v0.49.0 doesn't emit it.
func (a *API) issueOAuthCode(ctx context.Context, w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, userID, agentEmail string) error {
	sess := &oauth.Session{
		UserID:     userID,
		AgentEmail: agentEmail,
		Subject:    userID,
	}
	ar.SetSession(sess)
	// fosite drops the requested scope between authorize and the
	// issued tokens unless we explicitly grant it.
	for _, sc := range ar.GetRequestedScopes() {
		ar.GrantScope(sc)
	}

	resp, err := a.oauthProvider.NewAuthorizeResponse(ctx, ar, sess)
	if err != nil {
		return err
	}
	a.writeAuthorizeRedirect(w, r, ar, resp)
	return nil
}

// issueOAuthCodeWithNewAgent is the auto-create path: agent insert +
// code insert in a single pgx transaction. The tx is opened on the
// shared Storage pool; both writes join it via the oauth.WithTx
// context helper. A partial failure rolls back so we never leak an
// agent without the matching code (or vice versa).
func (a *API) issueOAuthCodeWithNewAgent(ctx context.Context, w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, userID, agentEmail string) error {
	pool := a.oauthStorage.Pool()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Safe to defer unconditionally: Rollback is a no-op after Commit
	// in pgx v5.
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := oauth.WithTx(ctx, tx)

	// Agent insert via the identity package — same tx, same context.
	if _, err := a.store.CreateAgentTx(txCtx, tx, agentEmail, a.sharedDomain, "", "", "local", userID); err != nil {
		if isUniqueViolation(err) {
			http.Error(w, "that slug is already taken; pick another", http.StatusConflict)
			return nil
		}
		return fmt.Errorf("create agent: %w", err)
	}

	// Code issue via fosite. Storage.db(txCtx) finds the tx on the
	// context, so the INSERT into oauth_auth_codes runs in the same tx.
	sess := &oauth.Session{
		UserID:     userID,
		AgentEmail: agentEmail,
		Subject:    userID,
	}
	ar.SetSession(sess)
	for _, sc := range ar.GetRequestedScopes() {
		ar.GrantScope(sc)
	}
	resp, err := a.oauthProvider.NewAuthorizeResponse(txCtx, ar, sess)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	a.writeAuthorizeRedirect(w, r, ar, resp)
	return nil
}

// writeAuthorizeRedirect emits the 303 redirect to the client's
// redirect_uri with fosite's parameters + the RFC 9207 issuer. We
// bypass fosite.WriteAuthorizeResponse so we can append `iss`;
// fosite v0.49.0 doesn't emit it natively.
func (a *API) writeAuthorizeRedirect(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, resp fosite.AuthorizeResponder) {
	redirect, err := url.Parse(ar.GetRedirectURI().String())
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := redirect.Query()
	for k, vs := range resp.GetParameters() {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	// RFC 9207 §2 — tell mix-up-aware clients which AS produced this
	// response. publicURL is what discovery advertises as `issuer`.
	q.Set("iss", strings.TrimRight(a.publicURL, "/"))
	redirect.RawQuery = q.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
}

// defaultAgentSlug derives a slug-safe default from the client_id.
// Used when the user clicks Allow without typing a custom slug. The
// 6-hex suffix gives 24 bits of collision resistance — plenty given
// uniqueness is checked at INSERT time on the shared domain.
func defaultAgentSlug(clientID string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)
	prefix := slugifyClientID(clientID)
	if prefix == "" {
		prefix = "agent"
	}
	out := prefix + "-" + suffix
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// slugifyClientID lowercases and strips a client_id to slug-safe chars.
// The "mcp_<hex>" client IDs we mint produce slugs like "mcp-abc123".
func slugifyClientID(clientID string) string {
	var b strings.Builder
	prev := byte(0)
	for i := 0; i < len(clientID); i++ {
		c := clientID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
		default:
			// Collapse runs of non-alnum into a single hyphen.
			if prev != '-' && b.Len() > 0 {
				b.WriteByte('-')
				prev = '-'
				continue
			}
		}
		if b.Len() > 0 {
			prev = b.String()[b.Len()-1]
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// isUniqueViolation reports whether err is a Postgres unique-violation
// (SQLSTATE 23505). Used by issueOAuthCodeWithNewAgent to surface slug
// collisions as a clean 409 to the user.
func isUniqueViolation(err error) bool {
	var pgErr interface {
		SQLState() string
	}
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
