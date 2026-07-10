package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
smtp:
  listen_addr: ":3025"
  domain: "test.e2a.dev"
http:
  listen_addr: ":9090"
database:
  url: "postgres://test:test@localhost/test"
signing:
  hmac_secret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
env: "production"
outbound_smtp:
  host: "smtp.example.com"
  port: 465
  from_domain: "mail.e2a.dev"
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.SMTP.ListenAddr != ":3025" {
		t.Errorf("SMTP.ListenAddr = %q, want :3025", cfg.SMTP.ListenAddr)
	}
	if cfg.SMTP.Domain != "test.e2a.dev" {
		t.Errorf("SMTP.Domain = %q, want test.e2a.dev", cfg.SMTP.Domain)
	}
	if cfg.HTTP.ListenAddr != ":9090" {
		t.Errorf("HTTP.ListenAddr = %q, want :9090", cfg.HTTP.ListenAddr)
	}
	if cfg.Database.URL != "postgres://test:test@localhost/test" {
		t.Errorf("Database.URL = %q", cfg.Database.URL)
	}
	if cfg.Signing.HMACSecret != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Errorf("Signing.HMACSecret = %q", cfg.Signing.HMACSecret)
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q, want production", cfg.Env)
	}
	if cfg.OutboundSMTP.Host != "smtp.example.com" {
		t.Errorf("OutboundSMTP.Host = %q", cfg.OutboundSMTP.Host)
	}
	if cfg.OutboundSMTP.Port != 465 {
		t.Errorf("OutboundSMTP.Port = %d, want 465", cfg.OutboundSMTP.Port)
	}
	if cfg.OutboundSMTP.FromDomain != "mail.e2a.dev" {
		t.Errorf("OutboundSMTP.FromDomain = %q", cfg.OutboundSMTP.FromDomain)
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
database:
  url: "postgres://original"
signing:
  hmac_secret: "original"
`), 0644)

	// Only secrets get env overrides
	t.Setenv("E2A_DATABASE_URL", "postgres://override")
	t.Setenv("E2A_HMAC_SECRET", "override-secret")
	t.Setenv("E2A_OUTBOUND_SMTP_USERNAME", "smtp-user")
	t.Setenv("E2A_OUTBOUND_SMTP_PASSWORD", "smtp-pass")
	// A non-PEM sentinel: the config layer only copies the string through to
	// cfg.OAuth.SigningKey (parsing happens later in agentauth.NewSigner), so
	// this needs no real key — and deliberately omits the "BEGIN ... PRIVATE
	// KEY" armor so secret scanners don't false-positive on a test fixture.
	t.Setenv("E2A_OAUTH_SIGNING_KEY", "signing-key-sentinel-not-a-real-pem")
	t.Setenv("E2A_OAUTH_SIGNING_KID", "k7")
	t.Setenv("E2A_WEBHOOK_INTERNAL_SINK_URL", "http://prober:8090/sink")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.OAuth.SigningKey == "" || cfg.OAuth.SigningKID != "k7" {
		t.Errorf("expected env override for OAuth signing key/kid, got key=%q kid=%q", cfg.OAuth.SigningKey, cfg.OAuth.SigningKID)
	}

	if cfg.Database.URL != "postgres://override" {
		t.Errorf("expected env override for Database.URL, got %q", cfg.Database.URL)
	}
	if cfg.Signing.HMACSecret != "override-secret" {
		t.Errorf("expected env override for HMACSecret, got %q", cfg.Signing.HMACSecret)
	}
	if cfg.OutboundSMTP.Username != "smtp-user" {
		t.Errorf("expected env override for OutboundSMTP.Username, got %q", cfg.OutboundSMTP.Username)
	}
	if cfg.OutboundSMTP.Password != "smtp-pass" {
		t.Errorf("expected env override for OutboundSMTP.Password, got %q", cfg.OutboundSMTP.Password)
	}
	if cfg.Webhook.InternalSinkURL != "http://prober:8090/sink" {
		t.Errorf("expected env override for Webhook.InternalSinkURL, got %q", cfg.Webhook.InternalSinkURL)
	}
}

func TestValidateProductionRejectsPlaceholderHMAC(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "production"
signing:
  hmac_secret: "change-me-in-production"
`), 0644)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load should refuse placeholder HMAC secret in production")
	}
}

func TestValidateProductionRejectsEmptyHMAC(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "production"
signing:
  hmac_secret: ""
`), 0644)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load should refuse empty HMAC secret in production")
	}
}

func TestValidateProductionRejectsShortHMAC(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "production"
signing:
  hmac_secret: "tooshort"
`), 0644)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load should refuse HMAC secret shorter than 32 bytes in production")
	}
}

func TestValidateProductionAcceptsLongHMAC(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "production"
signing:
  hmac_secret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
`), 0644)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load should accept 64-byte HMAC secret in production, got: %v", err)
	}
}

func TestValidateDevelopmentAllowsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "development"
signing:
  hmac_secret: "change-me-in-production"
`), 0644)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load should accept placeholder in development, got: %v", err)
	}
}

func TestIsProduction(t *testing.T) {
	prod := &Config{Env: "production"}
	dev := &Config{Env: "development"}

	if !prod.IsProduction() {
		t.Error("expected IsProduction() to return true for production")
	}
	if dev.IsProduction() {
		t.Error("expected IsProduction() to return false for development")
	}
}
