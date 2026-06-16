package agent_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/agentauth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/gorilla/mux"
)

// jwksServer builds an httptest server exposing /.well-known/jwks.json with the
// given signer wired (nil ⇒ none). No DB needed — JWKS doesn't touch storage.
func jwksServer(t *testing.T, signer *agentauth.Signer) *httptest.Server {
	t.Helper()
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(nil, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "https://test.e2a.dev", false)
	if signer != nil {
		api.SetSigner(signer)
	}
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func genPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// TestJWKS_Enabled: a configured signer publishes exactly its public key, with
// kid + use=sig + alg=RS256, and no private material.
func TestJWKS_Enabled(t *testing.T) {
	signer, err := agentauth.NewSigner(genPEM(t), "v1")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	srv := jwksServer(t, signer)

	resp, err := http.Get(srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if k["kid"] != "v1" || k["use"] != "sig" || k["alg"] != "RS256" || k["kty"] != "RSA" {
		t.Errorf("jwk header fields = %v, want kid=v1 use=sig alg=RS256 kty=RSA", k)
	}
	// Must NOT leak the private exponent.
	if _, leaked := k["d"]; leaked {
		t.Error("published JWK leaked private key material (field 'd')")
	}
}

// TestJWKS_Disabled: with no signing key, the endpoint still serves valid JSON
// with an empty key set (never 404), so verifiers get a definite "no keys".
func TestJWKS_Disabled(t *testing.T) {
	disabled, _ := agentauth.NewSigner("", "")
	srv := jwksServer(t, disabled)

	resp, err := http.Get(srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty set, not 404)", resp.StatusCode)
	}
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Keys) != 0 {
		t.Errorf("disabled JWKS keys = %d, want 0", len(doc.Keys))
	}
}
