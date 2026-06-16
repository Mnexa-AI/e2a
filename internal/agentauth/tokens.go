package agentauth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v3/jwt"
)

// auth.md token model (Slice 5b-2). e2a issues two server-signed RS256 JWTs:
//
//   - identity_assertion — the long-lived credential an agent holds (typ
//     "identity_assertion"). Minted at POST /agent/identity once the agent has
//     proven who it is (Slice 5b-2 bootstraps this with the agent's e2a_agt_
//     key). Re-presented at the token endpoint to mint access tokens.
//   - access_token — the short-lived bearer (typ "access_token") the agent
//     actually calls the API with. Minted at POST /oauth2/token via
//     grant_type=jwt-bearer.
//
// Both carry the agent's email as `sub`, the granted `scope` (always "agent" on
// this path), and the `assertion_version` of the agent row — the kill switch: a
// bump invalidates every assertion/token mintable from the old version.
const (
	TypIdentityAssertion = "identity_assertion"
	TypAccessToken       = "access_token"

	// IdentityAssertionTTL bounds the long-lived credential. Re-mintable any
	// time from the agent's e2a_agt_ key, so it need not be long; 30 days
	// matches the auth.md reference's anonymous assertion.
	IdentityAssertionTTL = 30 * 24 * time.Hour
	// AccessTokenTTL keeps the bearer short so a leak self-heals quickly. The
	// agent re-exchanges its identity_assertion when it expires.
	AccessTokenTTL = 15 * time.Minute

	// clockSkew tolerates small clock drift between signer and verifier.
	clockSkew = 60 * time.Second
)

// ErrTokenInvalid wraps every verification failure so callers can map the whole
// class to a 401/invalid_grant without leaking which check failed.
var ErrTokenInvalid = errors.New("agentauth: token invalid")

// tokenPrivateClaims are e2a's non-registered claims, embedded alongside the
// standard jwt.Claims (iss/sub/aud/exp/iat).
type tokenPrivateClaims struct {
	Type             string `json:"typ"`
	Scope            string `json:"scope"`
	AssertionVersion int    `json:"assertion_version"`
}

// VerifiedClaims is the validated, typed result of VerifyToken.
type VerifiedClaims struct {
	Subject          string
	Scope            string
	AssertionVersion int
	Type             string
	Expiry           time.Time
}

// SignIdentityAssertion mints a long-lived identity_assertion for sub (the
// agent email), bound to issuer (= the AS public URL, used as both iss and
// aud). Returns the token and its absolute expiry.
func (s *Signer) SignIdentityAssertion(sub, scope string, assertionVersion int, issuer string) (string, time.Time, error) {
	return s.signTyped(TypIdentityAssertion, sub, scope, assertionVersion, issuer, IdentityAssertionTTL)
}

// SignAccessToken mints a short-lived access_token for sub. Same issuer/aud
// binding as the assertion. Returns the token and its absolute expiry.
func (s *Signer) SignAccessToken(sub, scope string, assertionVersion int, issuer string) (string, time.Time, error) {
	return s.signTyped(TypAccessToken, sub, scope, assertionVersion, issuer, AccessTokenTTL)
}

func (s *Signer) signTyped(typ, sub, scope string, assertionVersion int, issuer string, ttl time.Duration) (string, time.Time, error) {
	if !s.Enabled() {
		return "", time.Time{}, ErrSigningDisabled
	}
	now := time.Now()
	exp := now.Add(ttl)
	std := jwt.Claims{
		Issuer:    issuer,
		Subject:   sub,
		Audience:  jwt.Audience{issuer},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(exp),
		ID:        generateJTI(),
	}
	priv := map[string]interface{}{
		"typ":               typ,
		"scope":             scope,
		"assertion_version": assertionVersion,
	}
	tok, err := s.Sign(std, priv)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// VerifyToken validates an e2a-minted JWT: RS256 signature against the local
// public key, iss == issuer, aud contains issuer, not expired (with skew), and
// typ == expectedType. On success returns the typed claims; every failure wraps
// ErrTokenInvalid. assertion_version freshness is the caller's job (it needs the
// live agent row).
func (s *Signer) VerifyToken(token, expectedType, issuer string) (*VerifiedClaims, error) {
	if !s.Enabled() {
		return nil, ErrSigningDisabled
	}
	parsed, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrTokenInvalid, err)
	}
	var std jwt.Claims
	var priv tokenPrivateClaims
	if err := parsed.Claims(s.priv.Public(), &std, &priv); err != nil {
		// Signature mismatch or malformed — invalid.
		return nil, fmt.Errorf("%w: signature: %v", ErrTokenInvalid, err)
	}
	if err := std.ValidateWithLeeway(jwt.Expected{
		Issuer:   issuer,
		Audience: jwt.Audience{issuer},
		Time:     time.Now(),
	}, clockSkew); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrTokenInvalid, err)
	}
	if priv.Type != expectedType {
		return nil, fmt.Errorf("%w: typ %q, want %q", ErrTokenInvalid, priv.Type, expectedType)
	}
	if std.Subject == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrTokenInvalid)
	}
	var exp time.Time
	if std.Expiry != nil {
		exp = std.Expiry.Time()
	}
	return &VerifiedClaims{
		Subject:          std.Subject,
		Scope:            priv.Scope,
		AssertionVersion: priv.AssertionVersion,
		Type:             priv.Type,
		Expiry:           exp,
	}, nil
}

// generateJTI returns a unique token id. crypto/rand failure panics (an
// all-zero jti would collide across tokens) — same posture as the key RNG.
func generateJTI() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("agentauth: crypto/rand failed: %v", err))
	}
	return "jti_" + hex.EncodeToString(b)
}
