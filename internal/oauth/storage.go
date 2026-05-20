package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/pkce"
)

// Compile-time interface assertions: *Storage must satisfy every
// fosite storage interface we plug into compose.Compose. If fosite
// adds methods between versions, the build fails here rather than at
// runtime when a /token request comes in.
var (
	_ fosite.Storage                  = (*Storage)(nil)
	_ fosite.ClientManager            = (*Storage)(nil)
	_ oauth2.CoreStorage              = (*Storage)(nil)
	_ oauth2.AuthorizeCodeStorage     = (*Storage)(nil)
	_ oauth2.AccessTokenStorage       = (*Storage)(nil)
	_ oauth2.RefreshTokenStorage      = (*Storage)(nil)
	_ oauth2.TokenRevocationStorage   = (*Storage)(nil)
	_ pkce.PKCERequestStorage         = (*Storage)(nil)
)

// ───────────────────────── ClientManager ─────────────────────────

// GetClient loads the client by ID. Returns fosite.ErrInvalidClient
// when the row doesn't exist so fosite's error mapping can produce the
// right RFC 6749 §5.2 error code at the endpoint.
func (s *Storage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var c Client
	c.ID = id
	var secretHash *string
	err := s.pool.QueryRow(ctx, `
		SELECT client_name, redirect_uris, grant_types, response_types,
		       scopes, audiences, token_endpoint_auth_method,
		       client_secret_hash, public
		FROM oauth_clients WHERE client_id = $1
	`, id).Scan(
		&c.Name, &c.RedirectURIs, &c.GrantTypeStrings, &c.ResponseTypeStrings,
		&c.ScopeStrings, &c.AudienceStrings, &c.TokenEndpointAuthMethodS,
		&secretHash, &c.Public,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrInvalidClient
	}
	if err != nil {
		return nil, err
	}
	if secretHash != nil {
		c.SecretHash = []byte(*secretHash)
	}
	return &c, nil
}

// ClientAssertionJWTValid is for the JWT-Bearer client authentication
// method (RFC 7523). We don't support that auth method — public
// clients only, no JWT assertions. Always return nil so the validity
// check passes vacuously; we'd reject the auth method earlier at the
// /token endpoint anyway.
func (s *Storage) ClientAssertionJWTValid(ctx context.Context, jti string) error {
	return nil
}

func (s *Storage) SetClientAssertionJWT(ctx context.Context, jti string, exp time.Time) error {
	return nil
}

// ───────────────────────── AuthorizeCodeStorage ─────────────────────────

// persistedRequest is what we serialize into the `request` JSONB
// column. Mirrors fosite.Request minus the session, which we round-trip
// via the caller-provided pointer in GetXxxSession.
type persistedRequest struct {
	ID                string          `json:"id"`
	RequestedAt       time.Time       `json:"requested_at"`
	ClientID          string          `json:"client_id"`
	RequestedScope    []string        `json:"requested_scope"`
	GrantedScope      []string        `json:"granted_scope"`
	Form              map[string][]string `json:"form"`
	RequestedAudience []string        `json:"requested_audience"`
	GrantedAudience   []string        `json:"granted_audience"`
	Session           json.RawMessage `json:"session"`
}

// marshalRequest serializes the fosite.Requester sans-session-instance
// for persistence. The session bytes are stashed alongside; on lookup
// we hand them to the caller's session pointer (see hydrate).
func marshalRequest(req fosite.Requester) ([]byte, error) {
	sessBytes, err := json.Marshal(req.GetSession())
	if err != nil {
		return nil, fmt.Errorf("marshal session: %w", err)
	}
	p := persistedRequest{
		ID:                req.GetID(),
		RequestedAt:       req.GetRequestedAt(),
		ClientID:          req.GetClient().GetID(),
		RequestedScope:    []string(req.GetRequestedScopes()),
		GrantedScope:      []string(req.GetGrantedScopes()),
		Form:              req.GetRequestForm(),
		RequestedAudience: []string(req.GetRequestedAudience()),
		GrantedAudience:   []string(req.GetGrantedAudience()),
		Session:           sessBytes,
	}
	return json.Marshal(p)
}

// hydrate decodes a persisted request row into a fresh fosite.Request,
// loading the linked Client from oauth_clients and unmarshaling the
// session bytes into the caller-provided session value.
func (s *Storage) hydrate(ctx context.Context, raw []byte, session fosite.Session) (fosite.Requester, error) {
	var p persistedRequest
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	if len(p.Session) > 0 && session != nil {
		if err := json.Unmarshal(p.Session, session); err != nil {
			return nil, fmt.Errorf("unmarshal session: %w", err)
		}
	}
	client, err := s.GetClient(ctx, p.ClientID)
	if err != nil {
		return nil, fmt.Errorf("hydrate client: %w", err)
	}
	r := &fosite.Request{
		ID:                p.ID,
		RequestedAt:       p.RequestedAt,
		Client:            client,
		RequestedScope:    fosite.Arguments(p.RequestedScope),
		GrantedScope:      fosite.Arguments(p.GrantedScope),
		Form:              p.Form,
		RequestedAudience: fosite.Arguments(p.RequestedAudience),
		GrantedAudience:   fosite.Arguments(p.GrantedAudience),
		Session:           session,
	}
	return r, nil
}

func (s *Storage) CreateAuthorizeCodeSession(ctx context.Context, code string, request fosite.Requester) error {
	raw, err := marshalRequest(request)
	if err != nil {
		return err
	}
	sess, _ := request.GetSession().(*Session)
	userID := ""
	if sess != nil {
		userID = sess.UserID
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_auth_codes
		    (signature, request_id, client_id, user_id, request,
		     requested_at, expires_at, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
	`, code, request.GetID(), request.GetClient().GetID(), userID,
		raw, request.GetRequestedAt(),
		// Authorize codes live 60s — fosite's default. We persist the
		// concrete expiry so DB-side reaping can act without recomputing.
		request.GetRequestedAt().Add(time.Minute),
	)
	return err
}

func (s *Storage) GetAuthorizeCodeSession(ctx context.Context, code string, session fosite.Session) (fosite.Requester, error) {
	var raw []byte
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT request, active FROM oauth_auth_codes WHERE signature = $1
	`, code).Scan(&raw, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	req, err := s.hydrate(ctx, raw, session)
	if err != nil {
		return nil, err
	}
	if !active {
		// Reuse detection: the row exists but was invalidated. fosite's
		// contract is to return BOTH the request and ErrInvalidatedAuthorizeCode
		// so the caller can revoke downstream tokens (RFC 6749 §10.5).
		return req, fosite.ErrInvalidatedAuthorizeCode
	}
	return req, nil
}

func (s *Storage) InvalidateAuthorizeCodeSession(ctx context.Context, code string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE oauth_auth_codes SET active = FALSE WHERE signature = $1`, code)
	return err
}

// ───────────────────────── AccessTokenStorage ─────────────────────────

func (s *Storage) CreateAccessTokenSession(ctx context.Context, signature string, request fosite.Requester) error {
	raw, err := marshalRequest(request)
	if err != nil {
		return err
	}
	sess, _ := request.GetSession().(*Session)
	userID := ""
	expiresAt := request.GetRequestedAt().Add(time.Hour) // fosite default
	if sess != nil {
		userID = sess.UserID
		if t := sess.GetExpiresAt(fosite.AccessToken); !t.IsZero() {
			expiresAt = t
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_access_tokens
		    (signature, request_id, client_id, user_id, request,
		     requested_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, signature, request.GetID(), request.GetClient().GetID(), userID,
		raw, request.GetRequestedAt(), expiresAt,
	)
	return err
}

func (s *Storage) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	var raw []byte
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT request, revoked_at FROM oauth_access_tokens WHERE signature = $1
	`, signature).Scan(&raw, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	req, err := s.hydrate(ctx, raw, session)
	if err != nil {
		return nil, err
	}
	if revokedAt != nil {
		return req, fosite.ErrInactiveToken
	}
	return req, nil
}

func (s *Storage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM oauth_access_tokens WHERE signature = $1`, signature)
	return err
}

// ───────────────────────── RefreshTokenStorage ─────────────────────────

func (s *Storage) CreateRefreshTokenSession(ctx context.Context, signature, accessSignature string, request fosite.Requester) error {
	raw, err := marshalRequest(request)
	if err != nil {
		return err
	}
	sess, _ := request.GetSession().(*Session)
	userID := ""
	if sess != nil {
		userID = sess.UserID
	}
	var accessSig *string
	if accessSignature != "" {
		accessSig = &accessSignature
	}
	// Refresh TTL: 30 days. fosite will set ExpiresAt on the session
	// if a non-default lifetime is configured; we use that when present.
	expiresAt := request.GetRequestedAt().Add(30 * 24 * time.Hour)
	if sess != nil {
		if t := sess.GetExpiresAt(fosite.RefreshToken); !t.IsZero() {
			expiresAt = t
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_refresh_tokens
		    (signature, request_id, client_id, user_id, access_signature,
		     request, requested_at, expires_at, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE)
	`, signature, request.GetID(), request.GetClient().GetID(), userID,
		accessSig, raw, request.GetRequestedAt(), expiresAt,
	)
	return err
}

func (s *Storage) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	var raw []byte
	var active bool
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT request, active, revoked_at FROM oauth_refresh_tokens WHERE signature = $1
	`, signature).Scan(&raw, &active, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	req, err := s.hydrate(ctx, raw, session)
	if err != nil {
		return nil, err
	}
	if !active || revokedAt != nil {
		// fosite contract: return both the Requester (so caller can
		// extract the request_id for chain revoke) and ErrInactiveToken.
		return req, fosite.ErrInactiveToken
	}
	return req, nil
}

func (s *Storage) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM oauth_refresh_tokens WHERE signature = $1`, signature)
	return err
}

// RotateRefreshToken marks the row as inactive but keeps it for reuse
// detection. fosite calls this on a successful refresh-grant exchange;
// a subsequent presenter of the same refresh sees active=false and
// fosite's handler fires the RFC 6749 §10.4 chain revoke.
func (s *Storage) RotateRefreshToken(ctx context.Context, requestID, refreshTokenSignature string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE oauth_refresh_tokens SET active = FALSE WHERE signature = $1`,
		refreshTokenSignature)
	return err
}

// ───────────────────────── TokenRevocationStorage ─────────────────────────

func (s *Storage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_refresh_tokens
		SET revoked_at = NOW(), active = FALSE
		WHERE request_id = $1 AND revoked_at IS NULL
	`, requestID)
	return err
}

func (s *Storage) RevokeAccessToken(ctx context.Context, requestID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_access_tokens
		SET revoked_at = NOW()
		WHERE request_id = $1 AND revoked_at IS NULL
	`, requestID)
	return err
}

// ───────────────────────── PKCERequestStorage ─────────────────────────

func (s *Storage) CreatePKCERequestSession(ctx context.Context, signature string, request fosite.Requester) error {
	raw, err := marshalRequest(request)
	if err != nil {
		return err
	}
	// PKCE sessions are short-lived; tie expiry to the auth-code window
	// (60s) plus a small grace so the token-exchange step doesn't race
	// the reaper.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_pkce_requests (signature, request_id, client_id, request, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, signature, request.GetID(), request.GetClient().GetID(), raw,
		request.GetRequestedAt().Add(2*time.Minute),
	)
	return err
}

func (s *Storage) GetPKCERequestSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT request FROM oauth_pkce_requests WHERE signature = $1`,
		signature).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrate(ctx, raw, session)
}

func (s *Storage) DeletePKCERequestSession(ctx context.Context, signature string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM oauth_pkce_requests WHERE signature = $1`, signature)
	return err
}
