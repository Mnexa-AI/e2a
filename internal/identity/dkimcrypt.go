package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// DKIM private keys live in domains.dkim_private_key. At rest they are
// envelope-encrypted with AES-256-GCM under a key derived from the master
// Signing.HMACSecret (#144 / M4) — a DB-read compromise must not yield usable
// "sign as any customer domain" material.
//
// The stored blob is self-describing so a read can tell encrypted from legacy
// plaintext without a schema change or a side flag column:
//
//	encrypted: dkimBlobV1 || nonce(12) || AES-256-GCM(DER, aad=domain)
//	legacy:    raw PKCS#1 DER — always begins 0x30 (ASN.1 SEQUENCE), never 0x01
//
// Because PKCS#1 DER never starts with 0x01, the version byte doubles as the
// encrypted/plaintext discriminator. That is what lets reads tolerate a
// half-migrated table while EncryptLegacyDKIMKeys backfills.
//
// OPERATIONAL CAVEATS (see runbook):
//   - Rotating Signing.HMACSecret re-derives a different KEK, making every stored
//     key undecryptable — signing then silently degrades to unsigned for all
//     domains. There is no re-key tooling yet; a key-versioned KEK + re-encrypt
//     pass is a prerequisite if secret rotation ever becomes operational. The
//     version byte is forward-compatible with that (a future 0x02 dual-read).
//   - Once the backfill encrypts the column, rolling back to a pre-#144 binary
//     sends mail unsigned + breaks BYODKIM provisioning until roll-forward (no
//     data loss — the bytes are still recoverable). Prefer roll-forward.
const (
	dkimBlobV1   byte = 0x01
	dkimKEKLabel      = "e2a-dkim-key-encryption-v1"
)

// DKIMCipher envelope-encrypts DKIM private keys at rest. Construct one with
// NewDKIMCipher and install it on the Store via SetDKIMCipher.
type DKIMCipher struct{ aead cipher.AEAD }

// NewDKIMCipher derives a dedicated 32-byte KEK from the master secret via
// HKDF-SHA256 (mirroring oauth.deriveOAuthSigningKey: a per-purpose label so the
// DKIM key can rotate independently of header/OAuth/HITL signing) and returns an
// AES-256-GCM cipher. It fails closed when the master is shorter than 32 bytes —
// the config layer only enforces that in production, so a weak dev secret must
// not silently produce weak at-rest encryption.
func NewDKIMCipher(master []byte) (*DKIMCipher, error) {
	if len(master) < 32 {
		return nil, fmt.Errorf("dkim: master secret is %d bytes, need ≥32 to derive a KEK", len(master))
	}
	kek := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, master, nil, []byte(dkimKEKLabel)), kek); err != nil {
		return nil, fmt.Errorf("dkim: derive KEK: %w", err)
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("dkim: aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("dkim: gcm: %w", err)
	}
	return &DKIMCipher{aead: aead}, nil
}

// seal encrypts a private key, binding the (normalized) domain as AAD so a
// ciphertext cannot be moved onto another domain's row and still decrypt.
func (c *DKIMCipher) seal(der []byte, domain string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("dkim: nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, der, []byte(domain))
	out := make([]byte, 0, 1+len(nonce)+len(ct))
	out = append(out, dkimBlobV1)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// open reverses seal. Callers pass a blob whose first byte is dkimBlobV1.
func (c *DKIMCipher) open(blob []byte, domain string) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < 1+n {
		return nil, errors.New("dkim: ciphertext too short")
	}
	der, err := c.aead.Open(nil, blob[1:1+n], blob[1+n:], []byte(domain))
	if err != nil {
		return nil, fmt.Errorf("dkim: decrypt: %w", err)
	}
	return der, nil
}
