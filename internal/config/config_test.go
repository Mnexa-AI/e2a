package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOutboundModeConfigurationRemoved is a source-level contract guard. Outbound
// delivery is always queue-first for GA, so neither the legacy environment switch
// nor its configuration model may quietly return in a later refactor.
func TestOutboundModeConfigurationRemoved(t *testing.T) {
	t.Helper()
	files := []string{
		"config.go",
		filepath.Join("..", "..", "cmd", "e2a", "main.go"),
		filepath.Join("..", "..", "config.example.yaml"),
	}
	forbidden := []string{"E2A_OUTBOUND_MODE", "OutboundConfig", "cfg.Outbound.Mode"}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(body), token) {
				t.Errorf("%s still contains removed outbound-mode token %q", path, token)
			}
		}
	}
}

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

func TestLoadConfigExternalAuthDefaultsDisabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`env: "development"`), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ExternalAuth.Enabled {
		t.Error("ExternalAuth.Enabled should default to false")
	}
}

func TestLoadConfigExternalAuthEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`env: "development"`), 0644)

	t.Setenv("E2A_EXTERNAL_AUTH_ENABLED", "true")
	t.Setenv("E2A_EXTERNAL_AUTH_ISSUER", "https://issuer.example.com")
	t.Setenv("E2A_EXTERNAL_AUTH_JWKS_URL", "https://issuer.example.com/.well-known/jwks.json")
	t.Setenv("E2A_EXTERNAL_AUTH_AUDIENCE", "e2a")
	t.Setenv("E2A_EXTERNAL_AUTH_USER_ID_CLAIM", "e2a_user_id")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.ExternalAuth.Enabled {
		t.Error("expected ExternalAuth.Enabled = true from env override")
	}
	if cfg.ExternalAuth.Issuer != "https://issuer.example.com" {
		t.Errorf("ExternalAuth.Issuer = %q", cfg.ExternalAuth.Issuer)
	}
	if cfg.ExternalAuth.JWKSURL != "https://issuer.example.com/.well-known/jwks.json" {
		t.Errorf("ExternalAuth.JWKSURL = %q", cfg.ExternalAuth.JWKSURL)
	}
	if cfg.ExternalAuth.Audience != "e2a" {
		t.Errorf("ExternalAuth.Audience = %q", cfg.ExternalAuth.Audience)
	}
	if cfg.ExternalAuth.UserIDClaim != "e2a_user_id" {
		t.Errorf("ExternalAuth.UserIDClaim = %q", cfg.ExternalAuth.UserIDClaim)
	}
}

func TestValidateExternalAuthEnabledRequiresAllFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "development"
external_auth:
  enabled: true
`), 0644)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Load should refuse external_auth.enabled with no issuer/jwks_url/audience/user_id_claim")
	}
	for _, want := range []string{"issuer", "jwks_url", "audience", "user_id_claim"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
}

func TestValidateExternalAuthEnabledAcceptsFullyConfigured(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "development"
external_auth:
  enabled: true
  issuer: "https://issuer.example.com"
  jwks_url: "https://issuer.example.com/.well-known/jwks.json"
  audience: "e2a"
  user_id_claim: "e2a_user_id"
`), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load should accept a fully configured external_auth block, got: %v", err)
	}
	if !cfg.ExternalAuth.Enabled {
		t.Error("expected ExternalAuth.Enabled = true")
	}
}

func TestValidateExternalAuthDisabledIgnoresEmptyFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
env: "development"
external_auth:
  enabled: false
`), 0644)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load should accept external_auth.enabled=false with empty fields, got: %v", err)
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

// Trash retention: defaults to 30 days, yaml + env override, and a value
// below 1 day is refused at startup (the stable API promises soft-deleted
// resources stay restorable — see internal/identity.TrashRetention).
func TestTrashRetentionDefaultOverrideAndValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Absent → default 30.
	cfg, err := Load(write("default.yaml", "env: \"development\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trash.RetentionDays != 30 {
		t.Errorf("default Trash.RetentionDays = %d, want 30", cfg.Trash.RetentionDays)
	}

	// YAML override.
	cfg, err = Load(write("yaml.yaml", "env: \"development\"\ntrash:\n  retention_days: 7\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trash.RetentionDays != 7 {
		t.Errorf("yaml Trash.RetentionDays = %d, want 7", cfg.Trash.RetentionDays)
	}

	// Env override wins over yaml.
	t.Setenv("E2A_TRASH_RETENTION_DAYS", "90")
	cfg, err = Load(write("env.yaml", "env: \"development\"\ntrash:\n  retention_days: 7\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trash.RetentionDays != 90 {
		t.Errorf("env Trash.RetentionDays = %d, want 90", cfg.Trash.RetentionDays)
	}
	t.Setenv("E2A_TRASH_RETENTION_DAYS", "")

	// Below 1 day → refused.
	if _, err := Load(write("zero.yaml", "env: \"development\"\ntrash:\n  retention_days: 0\n")); err == nil {
		t.Error("Load should reject trash.retention_days: 0")
	}
	if _, err := Load(write("neg.yaml", "env: \"development\"\ntrash:\n  retention_days: -3\n")); err == nil {
		t.Error("Load should reject a negative trash.retention_days")
	}
}
