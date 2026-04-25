package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// placeholderHMACSecret is the example value shipped in config.example.yaml.
// It must be overridden in any production deployment — the server refuses
// to start with this value when env: production.
const placeholderHMACSecret = "change-me-in-production"

// minHMACSecretBytes is the minimum HMAC secret length enforced in
// production. RFC 2104 §3 recommends keys be at least the output length
// of the hash function (32 bytes for SHA-256) — anything shorter weakens
// the MAC's security margin to brute-force range.
const minHMACSecretBytes = 32

type Config struct {
	SMTP         SMTPConfig         `yaml:"smtp"`
	HTTP         HTTPConfig         `yaml:"http"`
	Database     DatabaseConfig     `yaml:"database"`
	OAuth        OAuthConfig        `yaml:"oauth"`
	Signing      SigningConfig      `yaml:"signing"`
	OutboundSMTP OutboundSMTPConfig `yaml:"outbound_smtp"`
	Env          string             `yaml:"env"` // "development" or "production"
	// SharedDomain enables slug-based agent registration. When set
	// (e.g. "agents.example.com"), users can register agents with just a
	// slug and get `<slug>@<shared_domain>` provisioned without DNS
	// setup. Empty disables slug registration — every agent must use a
	// custom domain that the user owns and verifies. The shared domain
	// itself is reserved: it cannot be claimed as a custom domain.
	SharedDomain string `yaml:"shared_domain"`
}

func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

type SMTPConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	Domain     string `yaml:"domain"`
	TLSCert    string `yaml:"tls_cert"`
	TLSKey     string `yaml:"tls_key"`
}

type HTTPConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	// PublicURL is the externally visible base URL of the API, used to
	// build absolute links in notification emails (e.g. HITL magic-link
	// approve/reject). Example: "https://e2a.example.com". If empty,
	// features that need absolute URLs gracefully degrade.
	PublicURL string `yaml:"public_url"`
}

type DatabaseConfig struct {
	URL string `yaml:"url"`
}

type OAuthConfig struct {
	GoogleClientID     string `yaml:"google_client_id"`
	GoogleClientSecret string `yaml:"google_client_secret"`
	RedirectURL        string `yaml:"redirect_url"`
}

type SigningConfig struct {
	HMACSecret string `yaml:"hmac_secret"`
}

type OutboundSMTPConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	FromDomain string `yaml:"from_domain"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		SMTP: SMTPConfig{
			ListenAddr: ":2525",
			Domain:     "e2a.example.com",
		},
		HTTP: HTTPConfig{
			ListenAddr: ":8080",
		},
		Env: "development",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Env overrides — secrets only (never duplicated in yaml)
	if v := os.Getenv("E2A_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("E2A_HMAC_SECRET"); v != "" {
		cfg.Signing.HMACSecret = v
	}
	if v := os.Getenv("E2A_GOOGLE_CLIENT_ID"); v != "" {
		cfg.OAuth.GoogleClientID = v
	}
	if v := os.Getenv("E2A_GOOGLE_CLIENT_SECRET"); v != "" {
		cfg.OAuth.GoogleClientSecret = v
	}
	if v := os.Getenv("E2A_OAUTH_REDIRECT_URL"); v != "" {
		cfg.OAuth.RedirectURL = v
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_HOST"); v != "" {
		cfg.OutboundSMTP.Host = v
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.OutboundSMTP.Port = p
		}
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_USERNAME"); v != "" {
		cfg.OutboundSMTP.Username = v
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_PASSWORD"); v != "" {
		cfg.OutboundSMTP.Password = v
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_FROM_DOMAIN"); v != "" {
		cfg.OutboundSMTP.FromDomain = v
	}
	if v := os.Getenv("E2A_PUBLIC_URL"); v != "" {
		cfg.HTTP.PublicURL = v
	}
	if v := os.Getenv("E2A_SHARED_DOMAIN"); v != "" {
		cfg.SharedDomain = v
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate enforces invariants that must hold before the server starts.
// In production mode the placeholder HMAC secret, an empty secret, and
// secrets shorter than the hash output length are hard rejected —
// running with any of these lets attackers forge X-E2A-Auth-* headers
// and approve HITL messages.
func (c *Config) Validate() error {
	if c.IsProduction() {
		if c.Signing.HMACSecret == "" {
			return errors.New("config: signing.hmac_secret (or E2A_HMAC_SECRET) must be set when env=production")
		}
		if c.Signing.HMACSecret == placeholderHMACSecret {
			return fmt.Errorf("config: signing.hmac_secret is the example placeholder %q; override it before running env=production", placeholderHMACSecret)
		}
		if len(c.Signing.HMACSecret) < minHMACSecretBytes {
			return fmt.Errorf("config: signing.hmac_secret is %d bytes; production requires at least %d (run `openssl rand -hex 32` to generate)", len(c.Signing.HMACSecret), minHMACSecretBytes)
		}
	}
	return nil
}
