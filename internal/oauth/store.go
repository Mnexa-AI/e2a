// Package oauth implements the storage layer + HTTP handlers for the
// MCP OAuth 2.1 authorization server (.claude/design/mcp-system.md §4.3).
//
// This file holds the storage primitives. Higher-level flow logic
// (auth code reuse triggers token revocation, refresh chain rotation,
// etc.) is composed in the endpoint handlers from these primitives —
// the store is pure CRUD + atomic mutations, no policy.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Token / ID prefixes. Kept here as a single source of truth so the
// dispatch in identity.GetUserByBearer can grep for them.
const (
	ClientIDPrefix     = "mcp_"
	AuthCodePrefix     = "oace_"
	AccessTokenPrefix  = "ate2a_"
	RefreshTokenPrefix = "rte2a_"
	ChainIDPrefix      = "rch_"
)

// Lifetimes are spec-aligned defaults; the design doc justifies each.
const (
	AuthCodeLifetime       = 60 * time.Second
	AccessTokenLifetime    = 1 * time.Hour
	RefreshTokenLifetime   = 30 * 24 * time.Hour
)

// Client is an OAuth 2.1 registered client (Claude Code, Cursor, etc.).
// Public clients (PKCE-only) have empty ClientSecretHash; confidential
// clients store a hash, never plaintext.
type Client struct {
	ClientID         string
	ClientName       string
	RedirectURIs     []string
	ClientType       string // "public" | "confidential"
	ClientSecretHash string
	Metadata         json.RawMessage
	CreatedAt        time.Time
	CreatedVia       string // "dcr" | "admin"
}

// AuthorizationCode is a one-shot code issued at consent and exchanged
// at /api/oauth/token for an access/refresh pair.
type AuthorizationCode struct {
	Code                string
	ClientID            string
	UserID              string
	AgentEmail          string // empty if null in DB
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string // always "S256"
	Scope               string
	ExpiresAt           time.Time
	ConsumedAt          *time.Time // nil = not yet consumed
}

// Token represents one row in oauth_tokens (one access token + the
// refresh token it was paired with, if not yet rotated).
type Token struct {
	AccessToken      string
	RefreshToken     string // empty if rotated
	RefreshChainID   string
	ClientID         string
	UserID           string
	AgentEmail       string // empty if null
	Scope            string
	ExpiresAt        time.Time
	RefreshExpiresAt *time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
}

// IsActive returns true if the token has not been revoked and the
// access-token portion hasn't expired. Callers that care about refresh
// lifetime should check RefreshExpiresAt separately.
func (t *Token) IsActive(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// Store is the OAuth persistence layer. Wraps a pgxpool.Pool and
// exposes only the primitives the OAuth handlers need.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ───────────────────────── token / ID generation ─────────────────────────

// New64HexBytes returns the prefix + 32 hex chars (16 bytes of entropy
// from crypto/rand). 128 bits is more than enough for an opaque token.
func newPrefixed(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Same pattern as identity.generateID: panic on RNG failure.
		// Returning a non-random token would poison the database; a
		// 500 is the correct surface for a broken OS RNG.
		panic(fmt.Sprintf("oauth: crypto/rand failed: %v", err))
	}
	return prefix + hex.EncodeToString(b)
}

// NewClientID returns 'mcp_' + 12 hex chars (6 bytes / 48 bits).
// Client IDs are identifiers, not secrets — entropy just needs to be
// enough to avoid accidental collision.
func NewClientID() string { return newPrefixed(ClientIDPrefix, 6) }

// NewAuthCode returns 'oace_' + 32 hex chars (128 bits). One-shot,
// 60s lifetime — treated as a bearer credential per RFC 6749 §10.5.
func NewAuthCode() string { return newPrefixed(AuthCodePrefix, 16) }

// NewAccessToken returns 'ate2a_' + 32 hex chars (128 bits).
func NewAccessToken() string { return newPrefixed(AccessTokenPrefix, 16) }

// NewRefreshToken returns 'rte2a_' + 32 hex chars (128 bits).
func NewRefreshToken() string { return newPrefixed(RefreshTokenPrefix, 16) }

// NewChainID groups all access+refresh tokens that descend from the
// same authorization-code exchange. Used to revoke entire chains when
// a refresh token is reused (RFC 6749 §10.4 defense).
func NewChainID() string { return newPrefixed(ChainIDPrefix, 12) }

// ───────────────────────── clients ─────────────────────────

// RegisterClient inserts a new oauth_clients row. Caller is responsible
// for hashing client_secret beforehand if confidential.
func (s *Store) RegisterClient(ctx context.Context, c *Client) error {
	if c.Metadata == nil {
		c.Metadata = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, client_type,
		     client_secret_hash, metadata, created_via)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
	`, c.ClientID, c.ClientName, c.RedirectURIs, c.ClientType,
		c.ClientSecretHash, c.Metadata, c.CreatedVia)
	return err
}

// ErrClientNotFound is returned by GetClient when the client_id does
// not exist. Use errors.Is to detect.
var ErrClientNotFound = errors.New("oauth: client not found")

// GetClient looks up an OAuth client by its client_id.
func (s *Store) GetClient(ctx context.Context, clientID string) (*Client, error) {
	var c Client
	var secretHash *string
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_name, redirect_uris, client_type,
		       client_secret_hash, metadata, created_at, created_via
		FROM oauth_clients WHERE client_id = $1
	`, clientID).Scan(
		&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.ClientType,
		&secretHash, &c.Metadata, &c.CreatedAt, &c.CreatedVia,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrClientNotFound
	}
	if err != nil {
		return nil, err
	}
	if secretHash != nil {
		c.ClientSecretHash = *secretHash
	}
	return &c, nil
}

// ───────────────────────── authorization codes ─────────────────────────

// IssueCode inserts a fresh authorization code. expires_at must be set
// by the caller (typically time.Now().Add(AuthCodeLifetime)).
func (s *Store) IssueCode(ctx context.Context, c *AuthorizationCode) error {
	var agentEmail *string
	if c.AgentEmail != "" {
		agentEmail = &c.AgentEmail
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_authorization_codes
		    (code, client_id, user_id, agent_email, redirect_uri,
		     code_challenge, code_challenge_method, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, c.Code, c.ClientID, c.UserID, agentEmail, c.RedirectURI,
		c.CodeChallenge, c.CodeChallengeMethod, c.Scope, c.ExpiresAt)
	return err
}

// ConsumeResult disambiguates the outcome of AtomicConsumeCode.
type ConsumeResult int

const (
	ConsumeFresh           ConsumeResult = iota // just consumed by this call
	ConsumeAlreadyConsumed                      // a previous call already consumed — replay attempt
	ConsumeExpired                              // exists but expires_at passed
	ConsumeNotFound                             // no such code
)

// AtomicConsumeCode tries to mark a code consumed and return the row.
// The UPDATE is conditional on (consumed_at IS NULL AND expires_at >
// NOW()), so concurrent consume attempts can never both succeed.
//
// Returns ConsumeFresh + the code on success. On contention or replay,
// returns ConsumeAlreadyConsumed and the code so the caller can revoke
// downstream tokens per RFC 6749 §10.5. On expiry/missing, the code
// pointer may be nil.
func (s *Store) AtomicConsumeCode(ctx context.Context, code string) (*AuthorizationCode, ConsumeResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Conditional update: only this call wins.
	var (
		c               AuthorizationCode
		agentEmail      *string
		consumedAtRaw   *time.Time
	)
	err = tx.QueryRow(ctx, `
		UPDATE oauth_authorization_codes
		SET consumed_at = NOW()
		WHERE code = $1 AND consumed_at IS NULL AND expires_at > NOW()
		RETURNING code, client_id, user_id, agent_email, redirect_uri,
		          code_challenge, code_challenge_method, scope,
		          expires_at, consumed_at
	`, code).Scan(
		&c.Code, &c.ClientID, &c.UserID, &agentEmail, &c.RedirectURI,
		&c.CodeChallenge, &c.CodeChallengeMethod, &c.Scope,
		&c.ExpiresAt, &consumedAtRaw,
	)
	if err == nil {
		if agentEmail != nil {
			c.AgentEmail = *agentEmail
		}
		c.ConsumedAt = consumedAtRaw
		if err := tx.Commit(ctx); err != nil {
			return nil, 0, err
		}
		return &c, ConsumeFresh, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}

	// Update returned no rows. Disambiguate: was it not found, expired,
	// or already consumed? Use the same tx for a read-after-failed-write.
	now := time.Now()
	err = tx.QueryRow(ctx, `
		SELECT code, client_id, user_id, agent_email, redirect_uri,
		       code_challenge, code_challenge_method, scope,
		       expires_at, consumed_at
		FROM oauth_authorization_codes WHERE code = $1
	`, code).Scan(
		&c.Code, &c.ClientID, &c.UserID, &agentEmail, &c.RedirectURI,
		&c.CodeChallenge, &c.CodeChallengeMethod, &c.Scope,
		&c.ExpiresAt, &consumedAtRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Commit(ctx)
		return nil, ConsumeNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if agentEmail != nil {
		c.AgentEmail = *agentEmail
	}
	c.ConsumedAt = consumedAtRaw

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}

	if c.ConsumedAt != nil {
		return &c, ConsumeAlreadyConsumed, nil
	}
	if !c.ExpiresAt.After(now) {
		return &c, ConsumeExpired, nil
	}
	// Shouldn't happen — the UPDATE should have matched. Treat as
	// already-consumed defensively.
	return &c, ConsumeAlreadyConsumed, nil
}

// ───────────────────────── tokens ─────────────────────────

// IssueToken inserts an oauth_tokens row. Used both at code-exchange
// time (initial token issuance) and refresh time (a new row in the
// same RefreshChainID — caller separately nulls the prior row's
// refresh_token via RotateRefreshToken).
func (s *Store) IssueToken(ctx context.Context, t *Token) error {
	var agentEmail *string
	if t.AgentEmail != "" {
		agentEmail = &t.AgentEmail
	}
	var refreshToken *string
	if t.RefreshToken != "" {
		refreshToken = &t.RefreshToken
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_tokens
		    (access_token, refresh_token, refresh_chain_id, client_id,
		     user_id, agent_email, scope, expires_at, refresh_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, t.AccessToken, refreshToken, t.RefreshChainID, t.ClientID,
		t.UserID, agentEmail, t.Scope, t.ExpiresAt, t.RefreshExpiresAt)
	return err
}

// ErrTokenNotFound is returned by lookup methods when the token does
// not exist. Use errors.Is to detect.
var ErrTokenNotFound = errors.New("oauth: token not found")

// LookupTokenByAccess fetches the row for an access token, regardless
// of revoked/expired state. Callers must check IsActive() or the
// individual fields. This shape lets the auth middleware emit clear
// errors ("revoked" vs "expired") without re-querying.
func (s *Store) LookupTokenByAccess(ctx context.Context, accessToken string) (*Token, error) {
	return s.lookupToken(ctx, "access_token = $1", accessToken)
}

// LookupTokenByRefresh fetches the row by refresh_token. Returns
// ErrTokenNotFound when the refresh_token doesn't exist OR has been
// rotated (set NULL). Caller should not distinguish: both cases
// indicate a replayed or stale refresh.
func (s *Store) LookupTokenByRefresh(ctx context.Context, refreshToken string) (*Token, error) {
	return s.lookupToken(ctx, "refresh_token = $1", refreshToken)
}

func (s *Store) lookupToken(ctx context.Context, whereClause, arg string) (*Token, error) {
	var t Token
	var (
		refreshToken     *string
		agentEmail       *string
		refreshExpiresAt *time.Time
		revokedAt        *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT access_token, refresh_token, refresh_chain_id, client_id,
		       user_id, agent_email, scope, expires_at,
		       refresh_expires_at, revoked_at, created_at
		FROM oauth_tokens WHERE `+whereClause,
		arg,
	).Scan(
		&t.AccessToken, &refreshToken, &t.RefreshChainID, &t.ClientID,
		&t.UserID, &agentEmail, &t.Scope, &t.ExpiresAt,
		&refreshExpiresAt, &revokedAt, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	if refreshToken != nil {
		t.RefreshToken = *refreshToken
	}
	if agentEmail != nil {
		t.AgentEmail = *agentEmail
	}
	t.RefreshExpiresAt = refreshExpiresAt
	t.RevokedAt = revokedAt
	return &t, nil
}

// RotateRefreshToken issues a new token and atomically NULLs the prior
// refresh_token. The new token inherits the same RefreshChainID so a
// future reuse of any earlier refresh in this chain can revoke the
// whole family. Returns the newly-issued token's data after insert.
func (s *Store) RotateRefreshToken(ctx context.Context, oldRefresh string, newToken *Token) error {
	if newToken.RefreshChainID == "" {
		return errors.New("oauth: RotateRefreshToken requires RefreshChainID on new token")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := tx.Exec(ctx,
		`UPDATE oauth_tokens SET refresh_token = NULL WHERE refresh_token = $1`,
		oldRefresh,
	)
	if err != nil {
		return fmt.Errorf("invalidate old refresh: %w", err)
	}
	if res.RowsAffected() == 0 {
		// The refresh token didn't exist or was already rotated.
		// Caller (the /token endpoint) must catch this and revoke the
		// chain — that's the RFC 6749 §10.4 reuse defense. Returning
		// an error here surfaces the contract.
		return ErrTokenNotFound
	}

	var agentEmail *string
	if newToken.AgentEmail != "" {
		agentEmail = &newToken.AgentEmail
	}
	var refreshTokenArg *string
	if newToken.RefreshToken != "" {
		refreshTokenArg = &newToken.RefreshToken
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO oauth_tokens
		    (access_token, refresh_token, refresh_chain_id, client_id,
		     user_id, agent_email, scope, expires_at, refresh_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, newToken.AccessToken, refreshTokenArg, newToken.RefreshChainID,
		newToken.ClientID, newToken.UserID, agentEmail, newToken.Scope,
		newToken.ExpiresAt, newToken.RefreshExpiresAt,
	); err != nil {
		return fmt.Errorf("insert new token: %w", err)
	}

	return tx.Commit(ctx)
}

// RevokeToken marks a single access token revoked (sets revoked_at).
// Also NULLs the refresh_token so a refresh-grant attempt on this row
// fails the lookup. Used by /api/oauth/revoke (RFC 7009).
func (s *Store) RevokeToken(ctx context.Context, accessToken string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens
		SET revoked_at = NOW(), refresh_token = NULL
		WHERE access_token = $1 AND revoked_at IS NULL
	`, accessToken)
	return err
}

// RevokeChainByID marks every row in a refresh_chain_id revoked.
// Returns the number of rows updated. Caller of refresh-grant on a
// reused refresh token must call this with the chain_id loaded from
// the matching row to enforce RFC 6749 §10.4.
func (s *Store) RevokeChainByID(ctx context.Context, chainID string) (int, error) {
	res, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens
		SET revoked_at = NOW(), refresh_token = NULL
		WHERE refresh_chain_id = $1 AND revoked_at IS NULL
	`, chainID)
	if err != nil {
		return 0, err
	}
	return int(res.RowsAffected()), nil
}

// RevokeAllByClientUser marks every active token for a (client_id,
// user_id) pair revoked. Used when an authorization code is replayed
// (RFC 6749 §10.5) — we don't know exactly which tokens were issued
// from the compromised code, so we revoke the broader pair as a safe
// over-approximation. Returns the count.
func (s *Store) RevokeAllByClientUser(ctx context.Context, clientID, userID string) (int, error) {
	res, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens
		SET revoked_at = NOW(), refresh_token = NULL
		WHERE client_id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, clientID, userID)
	if err != nil {
		return 0, err
	}
	return int(res.RowsAffected()), nil
}
