package oauth

import (
	"context"
	"strings"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	enigma "github.com/ory/fosite/token/hmac"
)

// e2aPrefixedStrategy is fosite's HMAC-SHA token strategy with e2a-
// specific token prefixes glued on. fosite ships its own prefixed
// variant (`ory_at_…`, `ory_rt_…`, `ory_ac_…`) but the bearer dispatch
// in authenticateUser routes on `ate2a_`/`rte2a_`/`oace_` so we need
// our own conventions.
//
// The wrapping is purely cosmetic from fosite's perspective: generate
// produces the underlying token, we prepend the prefix before returning
// it to the client; validate strips the prefix before handing back to
// the underlying validator. Signature methods are inherited from
// HMACSHAStrategyUnPrefixed — they split the token on `.` and return
// the part after, so the prefix (which lives in the part before the
// `.`) is transparent to lookups by signature.
type e2aPrefixedStrategy struct {
	*oauth2.HMACSHAStrategyUnPrefixed
}

func newPrefixedStrategy(hmac *enigma.HMACStrategy, cfg oauth2.LifespanConfigProvider) *e2aPrefixedStrategy {
	return &e2aPrefixedStrategy{
		HMACSHAStrategyUnPrefixed: oauth2.NewHMACSHAStrategyUnPrefixed(hmac, cfg),
	}
}

// Compile-time assertion that we satisfy fosite's full CoreStrategy.
var _ oauth2.CoreStrategy = (*e2aPrefixedStrategy)(nil)

// ───────── Access tokens (ate2a_…) ─────────

func (s *e2aPrefixedStrategy) GenerateAccessToken(ctx context.Context, r fosite.Requester) (string, string, error) {
	tok, sig, err := s.HMACSHAStrategyUnPrefixed.GenerateAccessToken(ctx, r)
	if err != nil {
		return "", "", err
	}
	return AccessTokenPrefix + tok, sig, nil
}

func (s *e2aPrefixedStrategy) ValidateAccessToken(ctx context.Context, r fosite.Requester, token string) error {
	return s.HMACSHAStrategyUnPrefixed.ValidateAccessToken(ctx, r, strings.TrimPrefix(token, AccessTokenPrefix))
}

// ───────── Refresh tokens (rte2a_…) ─────────

func (s *e2aPrefixedStrategy) GenerateRefreshToken(ctx context.Context, r fosite.Requester) (string, string, error) {
	tok, sig, err := s.HMACSHAStrategyUnPrefixed.GenerateRefreshToken(ctx, r)
	if err != nil {
		return "", "", err
	}
	return RefreshTokenPrefix + tok, sig, nil
}

func (s *e2aPrefixedStrategy) ValidateRefreshToken(ctx context.Context, r fosite.Requester, token string) error {
	return s.HMACSHAStrategyUnPrefixed.ValidateRefreshToken(ctx, r, strings.TrimPrefix(token, RefreshTokenPrefix))
}

// ───────── Authorization codes (oace_…) ─────────

func (s *e2aPrefixedStrategy) GenerateAuthorizeCode(ctx context.Context, r fosite.Requester) (string, string, error) {
	tok, sig, err := s.HMACSHAStrategyUnPrefixed.GenerateAuthorizeCode(ctx, r)
	if err != nil {
		return "", "", err
	}
	return AuthCodePrefix + tok, sig, nil
}

func (s *e2aPrefixedStrategy) ValidateAuthorizeCode(ctx context.Context, r fosite.Requester, token string) error {
	return s.HMACSHAStrategyUnPrefixed.ValidateAuthorizeCode(ctx, r, strings.TrimPrefix(token, AuthCodePrefix))
}
