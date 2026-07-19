package agent

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v3/jwt"
)

// testAppKey generates a throwaway RSA key and returns it plus its
// base64-encoded PKCS#1 PEM (the canonical GITHUB_FEEDBACK_APP_PRIVATE_KEY
// env form).
func testAppKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return key, base64.StdEncoding.EncodeToString(pemBytes)
}

func TestParseGitHubAppKey(t *testing.T) {
	key, b64 := testAppKey(t)

	got, err := parseGitHubAppKey(b64)
	if err != nil {
		t.Fatalf("base64 form: %v", err)
	}
	if got.N.Cmp(key.N) != 0 {
		t.Error("base64 form: wrong key")
	}

	// Raw PEM with literal `\n` escapes (the other accepted env form).
	pemBytes, _ := base64.StdEncoding.DecodeString(b64)
	escaped := strings.ReplaceAll(string(pemBytes), "\n", `\n`)
	got, err = parseGitHubAppKey(escaped)
	if err != nil {
		t.Fatalf("escaped PEM form: %v", err)
	}
	if got.N.Cmp(key.N) != 0 {
		t.Error("escaped PEM form: wrong key")
	}

	if _, err := parseGitHubAppKey("not-a-key"); err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestExchangeInstallationToken(t *testing.T) {
	key, b64 := testAppKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/123/access_tokens" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok, err := jwt.ParseSigned(raw)
		if err != nil {
			t.Errorf("parse app jwt: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var claims jwt.Claims
		if err := tok.Claims(&key.PublicKey, &claims); err != nil {
			t.Errorf("verify app jwt signature: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if claims.Issuer != "456" {
			t.Errorf("iss = %q, want %q", claims.Issuer, "456")
		}
		if claims.Expiry == nil || claims.IssuedAt == nil {
			t.Error("app jwt missing iat/exp")
		} else if d := claims.Expiry.Time().Sub(claims.IssuedAt.Time()); d < 9*time.Minute || d > 11*time.Minute {
			t.Errorf("app jwt lifetime = %s, want ~10m", d)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"token": "ghs_test_token"})
	}))
	defer srv.Close()

	old := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = old }()

	tok, err := exchangeInstallationToken(context.Background(), "456", "123", b64)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tok != "ghs_test_token" {
		t.Errorf("token = %q, want %q", tok, "ghs_test_token")
	}
}

func TestExchangeInstallationToken_ErrorStatus(t *testing.T) {
	_, b64 := testAppKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	old := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = old }()

	if _, err := exchangeInstallationToken(context.Background(), "456", "999", b64); err == nil {
		t.Error("expected error on non-201 exchange response")
	}
}

func TestFeedbackGitHubClient_Precedence(t *testing.T) {
	// Hermetic env — none of the four vars leak in from the host.
	t.Setenv("GITHUB_FEEDBACK_APP_ID", "")
	t.Setenv("GITHUB_FEEDBACK_APP_INSTALLATION_ID", "")
	t.Setenv("GITHUB_FEEDBACK_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_FEEDBACK_TOKEN", "")

	// Nothing set → channel off.
	if c, err := feedbackGitHubClient(context.Background()); c != nil || err != nil {
		t.Errorf("no env: got client=%v err=%v, want nil,nil", c, err)
	}

	// PAT fallback.
	t.Setenv("GITHUB_FEEDBACK_TOKEN", "pat")
	if c, err := feedbackGitHubClient(context.Background()); c == nil || err != nil {
		t.Errorf("PAT: got client=%v err=%v, want client,nil", c, err)
	}

	// Partial app config → loud log, still PAT.
	t.Setenv("GITHUB_FEEDBACK_APP_ID", "456")
	if c, err := feedbackGitHubClient(context.Background()); c == nil || err != nil {
		t.Errorf("partial app config: got client=%v err=%v, want client,nil", c, err)
	}

	// Full app config with a bogus key → hard error, no silent PAT fallback.
	t.Setenv("GITHUB_FEEDBACK_APP_INSTALLATION_ID", "123")
	t.Setenv("GITHUB_FEEDBACK_APP_PRIVATE_KEY", "bogus")
	if c, err := feedbackGitHubClient(context.Background()); err == nil || c != nil {
		t.Errorf("bad app key: got client=%v err=%v, want nil,error", c, err)
	}
}
