// Package approvaltoken signs and verifies the short-lived HMAC tokens
// embedded in HITL notification-email magic links.
//
// A token authorizes exactly one action (approve or reject) on exactly one
// message and carries its own expiration. Tokens are URL-safe and contain
// no session state — verification depends only on the shared HMAC secret
// and the current time, so magic links work on any device the reviewer
// happens to be reading email on.
//
// Format:
//
//	base64url(payload) + "." + base64url(hmac_sha256(secret, payload))
//
// where payload is "<message_id>|<action>|<exp_unix>".
//
// The same config.Signing.HMACSecret used to sign X-E2A-Auth-* email
// headers is reused here, so there's no new key to rotate.
package approvaltoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Action values mirror the design-doc contract. Any other value in a
// verified token is treated as tampering.
const (
	ActionApprove = "approve"
	ActionReject  = "reject"
)

var (
	// ErrInvalidToken covers malformed, tampered, or unknown-action tokens.
	// Callers should treat all three the same — do not distinguish for
	// attackers.
	ErrInvalidToken = errors.New("invalid approval token")

	// ErrTokenExpired is returned when the signature is valid but the
	// embedded exp is in the past.
	ErrTokenExpired = errors.New("approval token expired")
)

// Claims is the verified payload of a magic-link token.
type Claims struct {
	MessageID string
	Action    string
	ExpiresAt time.Time
}

// Sign returns a URL-safe token signed with `secret`. action must be
// ActionApprove or ActionReject. exp sets the token's expiration —
// callers should pass a value slightly after the message's
// approval_expires_at so a click received just before TTL still works.
func Sign(secret, messageID, action string, exp time.Time) (string, error) {
	if action != ActionApprove && action != ActionReject {
		return "", fmt.Errorf("invalid action %q", action)
	}
	// The payload uses '|' as a separator and '\n' would confuse log
	// scanners. Defend by rejecting up-front; message IDs are generated
	// with hex characters only, so legitimate callers never hit this.
	if strings.ContainsAny(messageID, "|\n") {
		return "", fmt.Errorf("messageID contains reserved characters")
	}
	payload := fmt.Sprintf("%s|%s|%d", messageID, action, exp.Unix())
	sig := signMAC([]byte(secret), []byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify parses, HMAC-checks (against any of `secrets`), and exp-checks
// a token. Returns the claims on success; ErrInvalidToken for
// malformed / tampered / wrong-secret tokens; ErrTokenExpired for
// valid-but-past-exp tokens.
//
// Verify does not check that the message still exists or is still
// pending — that is the handler's job. Verify's only job is "was this
// string issued by us, and is its exp in the future".
//
// Accepting multiple secrets supports per-user multi-secret rotation:
// after a user creates a new secret, in-flight magic-link tokens issued
// under the old secret continue to verify until that secret is deleted.
func Verify(secrets []string, token string) (*Claims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	providedSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	matched := false
	for _, secret := range secrets {
		if hmac.Equal(providedSig, signMAC([]byte(secret), payloadBytes)) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, ErrInvalidToken
	}

	fields := strings.Split(string(payloadBytes), "|")
	if len(fields) != 3 {
		return nil, ErrInvalidToken
	}
	expUnix, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims := &Claims{
		MessageID: fields[0],
		Action:    fields[1],
		ExpiresAt: time.Unix(expUnix, 0),
	}
	if claims.Action != ActionApprove && claims.Action != ActionReject {
		return nil, ErrInvalidToken
	}
	if time.Now().After(claims.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	return claims, nil
}

// Signer is a thin single-secret wrapper kept for tests and the legacy
// deployment-wide signing path. New code should call Sign/Verify directly
// with the user's secrets pulled from the identity store.
type Signer struct {
	secret string
}

func NewSigner(secret string) *Signer {
	return &Signer{secret: secret}
}

func (s *Signer) Sign(messageID, action string, exp time.Time) (string, error) {
	return Sign(s.secret, messageID, action, exp)
}

func (s *Signer) Verify(token string) (*Claims, error) {
	return Verify([]string{s.secret}, token)
}

func signMAC(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// PeekMessageID extracts the message_id from a token *without* verifying
// the HMAC. Useful when the caller needs to look up the owning user's
// signing secrets to then call Verify with that secret list.
//
// SECURITY: the returned message_id is attacker-controlled until Verify
// confirms the signature. Use it only as a lookup hint to find which
// secrets to verify against — never act on the value before Verify
// returns claims successfully.
func PeekMessageID(token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", ErrInvalidToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalidToken
	}
	fields := strings.Split(string(payloadBytes), "|")
	if len(fields) != 3 {
		return "", ErrInvalidToken
	}
	if fields[0] == "" {
		return "", ErrInvalidToken
	}
	return fields[0], nil
}
