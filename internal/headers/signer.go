package headers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const DefaultMaxAge = 5 * time.Minute

const (
	HeaderVerified    = "X-E2A-Auth-Verified"
	HeaderSender      = "X-E2A-Auth-Sender"
	HeaderSignature   = "X-E2A-Auth-Signature"
	HeaderDelegation  = "X-E2A-Auth-Delegation"
	HeaderEntityType  = "X-E2A-Auth-Entity-Type"
	HeaderTimestamp   = "X-E2A-Auth-Timestamp"
	HeaderDomainCheck = "X-E2A-Auth-Domain-Check"
	HeaderMessageID   = "X-E2A-Auth-Message-Id"
	HeaderBodyHash    = "X-E2A-Auth-Body-Hash"
)

// HashBody returns the lowercase hex SHA-256 of the raw message body.
// Used both at sign time (to populate the canonical) and at verify time
// (so recipients can hash the bytes they received and compare to the
// signed canonical). Centralizing here ensures sender and verifier use
// identical encoding.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type AuthPayload struct {
	Verified    bool
	Sender      string
	EntityType  string // "human" or "agent"
	DomainCheck string // e.g. "spf=pass; dkim=none"
	AgentID     string
	HumanID     string
	// MessageID binds the signature to a specific message so a captured
	// (headers, MAC) pair cannot be lifted onto a different message
	// within the replay window. Required.
	MessageID string
	// BodyHash is the hex SHA-256 of the raw message bytes the recipient
	// will receive. Binding the MAC to the body hash prevents an
	// attacker from replaying valid headers under a modified body.
	// Callers should use HashBody(body) to compute it.
	BodyHash string
}

type AuthHeaders map[string]string

// Sign produces signed auth headers using the given HMAC secret. This
// is the canonical entry point — callers (the relay, in particular)
// look up the per-user secret and pass it in directly. The Signer
// struct below is a thin wrapper kept for tests and the legacy
// deployment-wide signing path.
func Sign(secret string, p AuthPayload) AuthHeaders {
	ts := time.Now().UTC().Format(time.RFC3339)
	verified := "false"
	if p.Verified {
		verified = "true"
	}

	delegation := ""
	if p.AgentID != "" && p.HumanID != "" {
		delegation = fmt.Sprintf("agent=%s;human=%s", p.AgentID, p.HumanID)
	}

	canonical := canonicalString(verified, p.Sender, p.EntityType, p.DomainCheck, delegation, ts, p.MessageID, p.BodyHash)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))

	h := AuthHeaders{
		HeaderVerified:    verified,
		HeaderSender:      p.Sender,
		HeaderEntityType:  p.EntityType,
		HeaderDomainCheck: p.DomainCheck,
		HeaderTimestamp:   ts,
		HeaderMessageID:   p.MessageID,
		HeaderBodyHash:    p.BodyHash,
		HeaderSignature:   sig,
	}
	if delegation != "" {
		h[HeaderDelegation] = delegation
	}
	return h
}

// Verify checks a header set against any of the provided secrets and
// the default replay window. Returns true if any secret produces a
// matching signature. Used by recipients holding multiple active keys
// during a rotation.
func Verify(secrets []string, h AuthHeaders) bool {
	return VerifyWithMaxAge(secrets, h, DefaultMaxAge)
}

// VerifyWithMaxAge is the configurable-window variant of Verify.
func VerifyWithMaxAge(secrets []string, h AuthHeaders, maxAge time.Duration) bool {
	sig := h[HeaderSignature]
	if sig == "" {
		return false
	}

	ts, err := time.Parse(time.RFC3339, h[HeaderTimestamp])
	if err != nil {
		return false
	}
	age := time.Since(ts)
	if age < -30*time.Second || age > maxAge {
		return false
	}

	canonical := canonicalString(
		h[HeaderVerified],
		h[HeaderSender],
		h[HeaderEntityType],
		h[HeaderDomainCheck],
		h[HeaderDelegation],
		h[HeaderTimestamp],
		h[HeaderMessageID],
		h[HeaderBodyHash],
	)

	for _, secret := range secrets {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return true
		}
	}
	return false
}

// Signer is a thin wrapper around a single secret. Kept for the legacy
// deployment-wide signing path used in tests and the contract server;
// new code should call Sign/Verify directly.
type Signer struct {
	secret []byte
}

func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret)}
}

func (s *Signer) Sign(p AuthPayload) AuthHeaders {
	return Sign(string(s.secret), p)
}

// canonicalString assembles the byte string fed to HMAC. Field order
// must match between Sign and Verify and is part of the wire contract;
// changing it is a breaking signature change.
func canonicalString(verified, sender, entityType, domainCheck, delegation, ts, messageID, bodyHash string) string {
	return strings.Join([]string{
		verified,
		sender,
		entityType,
		domainCheck,
		delegation,
		ts,
		messageID,
		bodyHash,
	}, "\n")
}

func (s *Signer) Verify(h AuthHeaders) bool {
	return Verify([]string{string(s.secret)}, h)
}

func (s *Signer) VerifyWithMaxAge(h AuthHeaders, maxAge time.Duration) bool {
	return VerifyWithMaxAge([]string{string(s.secret)}, h, maxAge)
}
