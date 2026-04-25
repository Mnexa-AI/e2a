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

type Signer struct {
	secret []byte
}

func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret)}
}

func (s *Signer) Sign(p AuthPayload) AuthHeaders {
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

	mac := hmac.New(sha256.New, s.secret)
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
	return s.VerifyWithMaxAge(h, DefaultMaxAge)
}

func (s *Signer) VerifyWithMaxAge(h AuthHeaders, maxAge time.Duration) bool {
	sig := h[HeaderSignature]
	if sig == "" {
		return false
	}

	// Replay protection: reject if timestamp is outside the allowed window
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

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expected))
}
