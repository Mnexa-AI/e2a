package identity_test

import (
	"context"
	"crypto/x509"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func dkimMaster() []byte {
	m := make([]byte, 32)
	for i := range m {
		m[i] = byte(i*7 + 3)
	}
	return m
}

func rawDKIMColumn(t *testing.T, pool *pgxpool.Pool, domain string) []byte {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT dkim_private_key FROM domains WHERE domain = $1`, domain).Scan(&raw); err != nil {
		t.Fatalf("read raw dkim column: %v", err)
	}
	return raw
}

// TestDKIMKeyEncryptedAtRest: with a cipher configured, ClaimOrCreateDomain stores
// the private key encrypted (tagged 0x01, not parseable as DER), yet both internal
// readers return the original, valid PKCS#1 key.
func TestDKIMKeyEncryptedAtRest(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	cipher, err := identity.NewDKIMCipher(dkimMaster())
	if err != nil {
		t.Fatalf("NewDKIMCipher: %v", err)
	}
	store := identity.NewStore(pool)
	store.SetDKIMCipher(cipher)

	user, err := store.CreateOrGetUser(ctx, "owner@enc.example.com", "Owner", "google-enc")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "enc.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	// Raw column is encrypted: tagged 0x01 (DER would be 0x30) and not parseable.
	raw := rawDKIMColumn(t, pool, "enc.example.com")
	if len(raw) == 0 || raw[0] != 0x01 {
		t.Fatalf("dkim_private_key not encrypted at rest: first byte = %#x", raw[0])
	}
	if _, err := x509.ParsePKCS1PrivateKey(raw); err == nil {
		t.Error("encrypted column should not parse as a PKCS#1 key")
	}

	// Both readers decrypt back to a valid key.
	for _, rd := range []struct {
		name string
		der  func() ([]byte, error)
	}{
		{"GetDKIMKeyInternal", func() ([]byte, error) {
			_, der, err := store.GetDKIMKeyInternal(ctx, "enc.example.com")
			return der, err
		}},
		{"SendingProvisionInputs", func() ([]byte, error) {
			_, der, _, err := store.SendingProvisionInputs(ctx, "enc.example.com")
			return der, err
		}},
	} {
		der, err := rd.der()
		if err != nil {
			t.Fatalf("%s: %v", rd.name, err)
		}
		if _, err := x509.ParsePKCS1PrivateKey(der); err != nil {
			t.Errorf("%s did not return a valid PKCS#1 key: %v", rd.name, err)
		}
	}
}

// TestDKIMKeyEncryptedNoCipherFailsClosed: a reader without the cipher must error
// on an encrypted row rather than hand back ciphertext as a key.
func TestDKIMKeyEncryptedNoCipherFailsClosed(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	cipher, _ := identity.NewDKIMCipher(dkimMaster())
	encStore := identity.NewStore(pool)
	encStore.SetDKIMCipher(cipher)
	user, err := encStore.CreateOrGetUser(ctx, "owner@nocipher.example.com", "Owner", "google-nc")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := encStore.ClaimOrCreateDomain(ctx, "nocipher.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	plainStore := identity.NewStore(pool) // no cipher
	if _, _, err := plainStore.GetDKIMKeyInternal(ctx, "nocipher.example.com"); err == nil {
		t.Error("reading an encrypted key without a cipher must fail closed")
	}
}

// TestEncryptLegacyDKIMKeysBackfill: a plaintext key written without a cipher is
// re-encrypted by the backfill, still decrypts, and the backfill is idempotent.
func TestEncryptLegacyDKIMKeysBackfill(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	// 1. Write a legacy plaintext key (no cipher).
	plainStore := identity.NewStore(pool)
	user, err := plainStore.CreateOrGetUser(ctx, "owner@legacy.example.com", "Owner", "google-legacy")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := plainStore.ClaimOrCreateDomain(ctx, "legacy.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if raw := rawDKIMColumn(t, pool, "legacy.example.com"); raw[0] != 0x30 {
		t.Fatalf("expected legacy plaintext DER (0x30), got first byte %#x", raw[0])
	}

	// 2. Backfill with a cipher.
	cipher, _ := identity.NewDKIMCipher(dkimMaster())
	encStore := identity.NewStore(pool)
	encStore.SetDKIMCipher(cipher)
	n, err := encStore.EncryptLegacyDKIMKeys(ctx)
	if err != nil {
		t.Fatalf("EncryptLegacyDKIMKeys: %v", err)
	}
	if n < 1 {
		t.Fatalf("backfill encrypted %d rows, want ≥1", n)
	}
	if raw := rawDKIMColumn(t, pool, "legacy.example.com"); raw[0] != 0x01 {
		t.Errorf("row not encrypted after backfill: first byte %#x", raw[0])
	}
	if _, der, err := encStore.GetDKIMKeyInternal(ctx, "legacy.example.com"); err != nil {
		t.Errorf("read after backfill: %v", err)
	} else if _, err := x509.ParsePKCS1PrivateKey(der); err != nil {
		t.Errorf("backfilled key no longer valid: %v", err)
	}

	// 3. Idempotent: a second run touches nothing.
	n2, err := encStore.EncryptLegacyDKIMKeys(ctx)
	if err != nil {
		t.Fatalf("EncryptLegacyDKIMKeys (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("second backfill encrypted %d rows, want 0 (idempotent)", n2)
	}
}
