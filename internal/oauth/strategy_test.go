package oauth

import (
	"context"
	"strings"
	"testing"

	"github.com/ory/fosite"
	enigma "github.com/ory/fosite/token/hmac"
)

// TestStrategy_SignatureIsPrefixTransparent pins the invariant that
// fosite's signature extraction returns the same value for the
// e2a-prefixed token and its unprefixed underlying form. Concretely:
// the token is `<base64-key>.<base64-sig>` and `AccessTokenSignature`
// is documented to return everything after the `.`. Our prefix lives
// in the part *before* the `.`, so adding it must not change what the
// strategy reports.
//
// If a future fosite version changes the signature extraction (e.g.,
// hashes the full token instead of splitting on `.`), this test fails
// and we know to revisit the prefix design before tokens silently stop
// validating.
func TestStrategy_SignatureIsPrefixTransparent(t *testing.T) {
	cfg := &fosite.Config{
		GlobalSecret:        []byte("test-secret-test-secret-test-sec"),
		AccessTokenLifespan: AccessTokenLifespan,
	}
	hmac := &enigma.HMACStrategy{Config: cfg}
	strategy := newPrefixedStrategy(hmac, cfg)
	ctx := context.Background()

	prefixed, sig, err := strategy.GenerateAccessToken(ctx, &fosite.Request{})
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	if !strings.HasPrefix(prefixed, AccessTokenPrefix) {
		t.Fatalf("generated token missing prefix: %q", prefixed)
	}
	unprefixed := strings.TrimPrefix(prefixed, AccessTokenPrefix)

	sigPrefixed := strategy.AccessTokenSignature(ctx, prefixed)
	sigUnprefixed := strategy.AccessTokenSignature(ctx, unprefixed)
	if sigPrefixed != sigUnprefixed {
		t.Errorf("signature differs between prefixed (%q) and unprefixed (%q) form",
			sigPrefixed, sigUnprefixed)
	}
	if sigPrefixed != sig {
		t.Errorf("AccessTokenSignature returned %q, want %q (Generate's signature)",
			sigPrefixed, sig)
	}
}
