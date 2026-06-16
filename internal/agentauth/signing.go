// Package agentauth is the auth.md agent-identity layer (api-v1-redesign §5 /
// Slice 5b). This file is the signing foundation (5b-1): an RS256 JWT signer
// loaded from a PEM private key, plus the public JWKS published at
// /.well-known/jwks.json that agents verify e2a-minted tokens against.
//
// Key management mirrors the sibling agentdrive deployment: the private key is
// supplied as a PEM string via config/env (E2A_OAUTH_SIGNING_KEY), never
// generated or persisted by e2a, with a `kid` (E2A_OAUTH_SIGNING_KID, default
// "v1") advertised in the JWKS and stamped on every token. An empty key leaves
// the agent-auth surface DISABLED: the JWKS serves an empty key set and any
// attempt to sign returns ErrSigningDisabled, so dev/self-host deployments that
// don't use agent identity run unchanged. Rotation = advertise a new kid, then
// retire the old after the longest token TTL.
package agentauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
)

// SigningAlg is the only algorithm e2a issues — RS256, what auth.md's reference
// implementation emits and what every JWKS consumer expects.
const SigningAlg = jose.RS256

// ErrSigningDisabled is returned by Sign when no signing key is configured.
// Handlers translate it into a 501/"agent auth not enabled" response rather
// than a 500, so an unconfigured deployment fails cleanly.
var ErrSigningDisabled = errors.New("agentauth: signing key not configured")

// Signer holds the parsed RSA private key and its key id. The zero value (and a
// Signer built from an empty PEM) is a valid, DISABLED signer.
type Signer struct {
	priv *rsa.PrivateKey
	kid  string
}

// NewSigner builds a Signer from a PEM-encoded RSA private key (PKCS#1 or
// PKCS#8) and a kid. An empty pemKey yields a disabled signer (no error) so the
// surface can be off by default; a non-empty but malformed key is a hard error
// so a misconfigured deployment fails fast at startup rather than at first sign.
func NewSigner(pemKey, kid string) (*Signer, error) {
	if pemKey == "" {
		return &Signer{}, nil
	}
	if kid == "" {
		kid = "v1"
	}
	priv, err := parseRSAPrivateKey(pemKey)
	if err != nil {
		return nil, err
	}
	return &Signer{priv: priv, kid: kid}, nil
}

// Enabled reports whether a signing key is configured.
func (s *Signer) Enabled() bool { return s != nil && s.priv != nil }

// KeyID returns the kid stamped on issued tokens (empty when disabled).
func (s *Signer) KeyID() string {
	if !s.Enabled() {
		return ""
	}
	return s.kid
}

// Sign issues an RS256 JWT carrying the given claims, with the kid in the
// header so verifiers can select the right JWKS entry. Returns
// ErrSigningDisabled when no key is configured.
func (s *Signer) Sign(claims jwt.Claims, private map[string]interface{}) (string, error) {
	if !s.Enabled() {
		return "", ErrSigningDisabled
	}
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: SigningAlg, Key: jose.JSONWebKey{Key: s.priv, KeyID: s.kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("agentauth: build signer: %w", err)
	}
	builder := jwt.Signed(sig).Claims(claims)
	if len(private) > 0 {
		builder = builder.Claims(private)
	}
	out, err := builder.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("agentauth: serialize jwt: %w", err)
	}
	return out, nil
}

// PublicJWKS returns the public half as a JWK set for /.well-known/jwks.json.
// When disabled it returns an empty (non-nil) set so the endpoint always serves
// valid JSON ({"keys":[]}).
func (s *Signer) PublicJWKS() jose.JSONWebKeySet {
	if !s.Enabled() {
		return jose.JSONWebKeySet{Keys: []jose.JSONWebKey{}}
	}
	return jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       s.priv.Public(),
		KeyID:     s.kid,
		Algorithm: string(SigningAlg),
		Use:       "sig",
	}}}
}

// parseRSAPrivateKey accepts PKCS#1 ("RSA PRIVATE KEY") or PKCS#8 ("PRIVATE
// KEY") PEM blocks and returns the RSA key. A PKCS#8 block carrying a non-RSA
// key is rejected (RS256 requires RSA).
func parseRSAPrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("agentauth: signing key is not valid PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("agentauth: parse private key (tried PKCS#1 and PKCS#8): %w", err)
		}
		rsaKey, ok := keyAny.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("agentauth: signing key is %T, want *rsa.PrivateKey (RS256)", keyAny)
		}
		key = rsaKey
	}
	// Reject weak moduli: RS256 with a < 2048-bit key is forgeable. A
	// misconfigured operator key must fail CLOSED at startup, not silently sign
	// with a breakable key (minMODBits matches NIST SP 800-131A).
	if key.N.BitLen() < minModulusBits {
		return nil, fmt.Errorf("agentauth: RSA key is %d-bit, want >= %d (RS256)", key.N.BitLen(), minModulusBits)
	}
	return key, nil
}

// minModulusBits is the smallest RSA modulus accepted for RS256 signing.
const minModulusBits = 2048
