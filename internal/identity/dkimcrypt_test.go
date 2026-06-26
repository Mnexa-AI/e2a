package identity

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"strings"
	"testing"
)

func testMaster(t *testing.T) []byte {
	t.Helper()
	m := make([]byte, 32)
	for i := range m {
		m[i] = byte(i + 1)
	}
	return m
}

func TestDKIMCipherRoundTrip(t *testing.T) {
	c, err := NewDKIMCipher(testMaster(t))
	if err != nil {
		t.Fatalf("NewDKIMCipher: %v", err)
	}
	plaintext := []byte("PKCS1-DER-private-key-bytes")
	blob, err := c.seal(plaintext, "acme.com")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Self-describing: version tag, and the ciphertext is not the plaintext.
	if blob[0] != dkimBlobV1 {
		t.Errorf("blob[0] = %#x, want version tag %#x", blob[0], dkimBlobV1)
	}
	if bytes.Contains(blob, plaintext) {
		t.Error("sealed blob leaks the plaintext key")
	}
	got, err := c.open(blob, "acme.com")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestDKIMCipherWrongDomainAAD(t *testing.T) {
	// The domain is bound as AAD, so a blob can't be moved to another domain row.
	c, _ := NewDKIMCipher(testMaster(t))
	blob, _ := c.seal([]byte("key"), "acme.com")
	if _, err := c.open(blob, "evil.com"); err == nil {
		t.Error("open with a different domain AAD must fail")
	}
}

func TestDKIMCipherTamperDetected(t *testing.T) {
	c, _ := NewDKIMCipher(testMaster(t))
	blob, _ := c.seal([]byte("key"), "acme.com")
	blob[len(blob)-1] ^= 0xff // flip a ciphertext/tag byte
	if _, err := c.open(blob, "acme.com"); err == nil {
		t.Error("open of a tampered blob must fail (GCM auth)")
	}
}

func TestDKIMCipherWrongKEK(t *testing.T) {
	c1, _ := NewDKIMCipher(testMaster(t))
	other := testMaster(t)
	other[0] ^= 0xff
	c2, _ := NewDKIMCipher(other)
	blob, _ := c1.seal([]byte("key"), "acme.com")
	if _, err := c2.open(blob, "acme.com"); err == nil {
		t.Error("open under a different KEK must fail")
	}
}

func TestNewDKIMCipherShortMasterFailsClosed(t *testing.T) {
	if _, err := NewDKIMCipher([]byte("too-short")); err == nil {
		t.Error("NewDKIMCipher must reject a <32-byte master")
	}
}

func TestDKIMCipherNonceIsRandom(t *testing.T) {
	c, _ := NewDKIMCipher(testMaster(t))
	b1, _ := c.seal([]byte("key"), "acme.com")
	b2, _ := c.seal([]byte("key"), "acme.com")
	if bytes.Equal(b1, b2) {
		t.Error("two seals of the same input must differ (random nonce)")
	}
}

// TestStoreUnsealDKIMLegacyPassthrough: a plaintext PKCS#1 DER row (first byte
// 0x30, never the 0x01 tag) passes through unchanged, so reads tolerate a
// half-migrated table.
func TestStoreUnsealDKIMLegacyPassthrough(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024) // small key: test speed only
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	if der[0] != 0x30 {
		t.Fatalf("expected DER to start 0x30, got %#x", der[0])
	}
	s := &Store{} // no cipher configured
	got, err := s.unsealDKIM(der, "acme.com")
	if err != nil {
		t.Fatalf("unsealDKIM(legacy): %v", err)
	}
	if !bytes.Equal(got, der) {
		t.Error("legacy plaintext DER must pass through unchanged")
	}
}

// TestStoreUnsealDKIMEncryptedWithoutCipherFailsClosed: an encrypted blob with no
// cipher configured must error, never be returned as if it were a key.
func TestStoreUnsealDKIMEncryptedWithoutCipherFailsClosed(t *testing.T) {
	c, _ := NewDKIMCipher(testMaster(t))
	blob, _ := c.seal([]byte("key"), "acme.com")
	s := &Store{} // no cipher
	if _, err := s.unsealDKIM(blob, "acme.com"); err == nil || !strings.Contains(err.Error(), "no cipher") {
		t.Errorf("expected fail-closed 'no cipher' error, got %v", err)
	}
}

// TestStoreSealUnsealDKIM: the store helpers round-trip through the configured
// cipher and the stored form is encrypted (tagged, not the plaintext).
func TestStoreSealUnsealDKIM(t *testing.T) {
	c, _ := NewDKIMCipher(testMaster(t))
	s := &Store{}
	s.SetDKIMCipher(c)
	der := []byte("PKCS1-DER")
	sealed, err := s.sealDKIM(der, "acme.com")
	if err != nil {
		t.Fatalf("sealDKIM: %v", err)
	}
	if sealed[0] != dkimBlobV1 || bytes.Equal(sealed, der) {
		t.Error("sealDKIM must return an encrypted, tagged blob")
	}
	got, err := s.unsealDKIM(sealed, "acme.com")
	if err != nil {
		t.Fatalf("unsealDKIM: %v", err)
	}
	if !bytes.Equal(got, der) {
		t.Error("store seal/unseal round-trip mismatch")
	}
}

// TestStoreSealDKIMNoCipherPlaintext: without a cipher the helper stores plaintext.
func TestStoreSealDKIMNoCipherPlaintext(t *testing.T) {
	s := &Store{}
	der := []byte{0x30, 0x01, 0x02}
	got, err := s.sealDKIM(der, "acme.com")
	if err != nil {
		t.Fatalf("sealDKIM: %v", err)
	}
	if !bytes.Equal(got, der) {
		t.Error("no cipher ⇒ sealDKIM must return plaintext unchanged")
	}
}
