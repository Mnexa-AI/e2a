// Package unsubscribe derives opaque, stable identifiers for managed
// unsubscribe scopes and their database lookup keys.
package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

const (
	tokenPrefix  = "u1_"
	tokenPurpose = "e2a:unsubscribe:u1"
	tokenLength  = len(tokenPrefix) + 43 // unpadded base64url SHA-256 digest
)

// Derive returns a deterministic opaque token bound to one user, sending
// agent, and recipient. Email identifiers use the same lowercase, trimmed
// lookup form used elsewhere in the service.
func Derive(secret, userID, agentID, recipient string) (string, error) {
	userID = strings.TrimSpace(userID)
	agentID = normalizeEmail(agentID)
	recipient = normalizeEmail(recipient)

	switch {
	case secret == "":
		return "", errors.New("unsubscribe token secret is required")
	case userID == "":
		return "", errors.New("unsubscribe token user ID is required")
	case agentID == "":
		return "", errors.New("unsubscribe token agent ID is required")
	case recipient == "":
		return "", errors.New("unsubscribe token recipient is required")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	writeField(mac, tokenPurpose)
	writeField(mac, userID)
	writeField(mac, agentID)
	writeField(mac, recipient)

	return tokenPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Hash returns the fixed-width key used to resolve a token without storing
// the bearer token itself.
func Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ValidToken reports whether token has the public wire format emitted by
// Derive. It validates shape only; authority still requires resolving Hash.
func ValidToken(token string) bool {
	if len(token) != tokenLength || !strings.HasPrefix(token, tokenPrefix) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(token[len(tokenPrefix):])
	return err == nil
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeField(dst byteWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = dst.Write(length[:])
	_, _ = dst.Write([]byte(value))
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
