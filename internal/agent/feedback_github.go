package agent

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/google/go-github/v72/github"
)

// These are vars so tests can point the client at an httptest server and use a
// short deadline without slowing the package suite.
var (
	githubAPIBaseURL      = "https://api.github.com"
	feedbackGitHubTimeout = 10 * time.Second
)

// feedbackGitHubClient resolves the GitHub credential for the feedback →
// issue channel, in precedence order:
//
//  1. GitHub App — GITHUB_FEEDBACK_APP_ID + GITHUB_FEEDBACK_APP_INSTALLATION_ID
//     + GITHUB_FEEDBACK_APP_PRIVATE_KEY all set: sign an app JWT and exchange
//     it for an installation access token. The token is minted fresh per
//     submission — feedback volume is far too low to need a token cache.
//  2. PAT fallback — GITHUB_FEEDBACK_TOKEN (a static token; what self-hosters
//     typically set).
//
// A partially-set App config falls through to the PAT with a loud log line —
// a typo'd var name must not silently strand the deployment on the lesser
// credential. (nil, nil) means the channel is off.
func feedbackGitHubClient(ctx context.Context) (*github.Client, error) {
	appID := os.Getenv("GITHUB_FEEDBACK_APP_ID")
	instID := os.Getenv("GITHUB_FEEDBACK_APP_INSTALLATION_ID")
	privKey := os.Getenv("GITHUB_FEEDBACK_APP_PRIVATE_KEY")

	if appID != "" && instID != "" && privKey != "" {
		tok, err := exchangeInstallationToken(ctx, appID, instID, privKey)
		if err != nil {
			return nil, fmt.Errorf("github app auth: %w", err)
		}
		return newFeedbackGitHubClient(tok)
	}
	if appID != "" || instID != "" || privKey != "" {
		log.Printf("feedback: GITHUB_FEEDBACK_APP_* partially set (need APP_ID, APP_INSTALLATION_ID, APP_PRIVATE_KEY) — falling back to GITHUB_FEEDBACK_TOKEN")
	}

	if pat := os.Getenv("GITHUB_FEEDBACK_TOKEN"); pat != "" {
		return newFeedbackGitHubClient(pat)
	}
	return nil, nil
}

func newFeedbackGitHubClient(token string) (*github.Client, error) {
	client := github.NewClient(&http.Client{Timeout: feedbackGitHubTimeout})
	baseURL, err := url.Parse(strings.TrimRight(githubAPIBaseURL, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("github api base url: %w", err)
	}
	client.BaseURL = baseURL
	return client.WithAuthToken(token), nil
}

// exchangeInstallationToken signs a short-lived RS256 app JWT and exchanges it
// for an installation access token scoped to the repos the installation sees.
// Mirrors the agentauth signing idiom (go-jose RS256, compact JWT).
func exchangeInstallationToken(ctx context.Context, appID, installationID, privKey string) (string, error) {
	key, err := parseGitHubAppKey(privKey)
	if err != nil {
		return "", err
	}

	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("build signer: %w", err)
	}
	now := time.Now()
	appJWT, err := jwt.Signed(sig).Claims(jwt.Claims{
		Issuer:   appID,
		IssuedAt: jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock-skew margin
		Expiry:   jwt.NewNumericDate(now.Add(9 * time.Minute)),   // GitHub caps JWTs at 10m
	}).CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", githubAPIBaseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: feedbackGitHubTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("token exchange: status %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("token exchange: decode: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("token exchange: empty token in response")
	}
	return out.Token, nil
}

// parseGitHubAppKey decodes the app private key from env form: either
// base64-encoded PEM (canonical — survives .env / compose quoting) or raw PEM
// with literal `\n` escapes.
func parseGitHubAppKey(raw string) (*rsa.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	var pemBytes []byte
	if strings.Contains(raw, "-----BEGIN") {
		pemBytes = []byte(strings.ReplaceAll(raw, `\n`, "\n"))
	} else {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("app private key: neither PEM nor base64: %w", err)
		}
		pemBytes = b
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("app private key: no PEM block found")
	}
	// GitHub emits PKCS#1 ("BEGIN RSA PRIVATE KEY"); accept PKCS#8 too.
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("app private key: parse: %w", err)
	}
	rsaKey, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app private key: not an RSA key")
	}
	return rsaKey, nil
}
