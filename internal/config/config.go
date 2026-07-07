package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// splitAndTrim splits a comma-separated env value into a clean slice (trimmed,
// no empties). Used for list-valued overrides like SNS topic ARNs.
func splitAndTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

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
	SMTP             SMTPConfig             `yaml:"smtp"`
	HTTP             HTTPConfig             `yaml:"http"`
	Database         DatabaseConfig         `yaml:"database"`
	OAuth            OAuthConfig            `yaml:"oauth"`
	Signing          SigningConfig          `yaml:"signing"`
	OutboundSMTP     OutboundSMTPConfig     `yaml:"outbound_smtp"`
	Outbound         OutboundConfig         `yaml:"outbound"`
	SenderIdentity   SenderIdentityConfig   `yaml:"sender_identity"`
	DeliveryFeedback DeliveryFeedbackConfig `yaml:"delivery_feedback"`
	Limits           LimitsConfig           `yaml:"limits"`
	Env              string                 `yaml:"env"` // "development" or "production"
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
	// PublicURL is the externally visible base URL of the *web app* — the
	// domain that serves the dashboard, HITL magic-link pages, and the
	// OAuth login/consent UI. Absolute links in notification emails and the
	// OAuth authorization_endpoint are built from it. Example:
	// "https://e2a.example.com". If empty, features that need absolute URLs
	// gracefully degrade.
	PublicURL string `yaml:"public_url"`
	// APIURL is the externally visible base URL of the *programmatic API* —
	// the host the SDKs/MCP target (e.g. "https://api.e2a.dev"). It is the
	// OAuth issuer identity and the base for the token/registration/
	// revocation/jwks endpoints, so it should match the host the MCP
	// resource is served from (RFC 9728: clients expect the issuer to live
	// with the API). Defaults to PublicURL when unset, so single-host
	// deployments and self-hosters need not set it.
	APIURL string `yaml:"api_url"`
}

type DatabaseConfig struct {
	URL string `yaml:"url"`
}

type OAuthConfig struct {
	GoogleClientID     string `yaml:"google_client_id"`
	GoogleClientSecret string `yaml:"google_client_secret"`
	RedirectURL        string `yaml:"redirect_url"`
	// SigningKey is the PEM-encoded RSA private key (PKCS#1 or PKCS#8) used to
	// sign auth.md agent-identity JWTs + access tokens (Slice 5b). The public
	// half is published at /.well-known/jwks.json. Empty ⇒ the agent-auth
	// surface is disabled (JWKS serves an empty set). Supplied via
	// E2A_OAUTH_SIGNING_KEY; never generated or persisted by e2a.
	SigningKey string `yaml:"signing_key"`
	// SigningKID is the key id advertised in the JWKS and stamped on every
	// issued JWT (E2A_OAUTH_SIGNING_KID; default "v1"). Rotation advertises a
	// new kid, then retires the old after the longest token TTL.
	SigningKID string `yaml:"signing_kid"`
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
	// RequireTLS fails the send if STARTTLS can't be negotiated, instead
	// of silently relaying in cleartext (a network attacker can strip the
	// STARTTLS capability from the server's EHLO to force this). Pointer
	// so an unset value can default to true in production while staying
	// off for dev relays (e.g. Mailpit on :1025 with no TLS). Regardless
	// of this flag, PLAIN auth is never sent over a cleartext connection.
	RequireTLS *bool `yaml:"require_tls"`
}

// OutboundConfig selects the outbound send execution model (async-send
// pipeline, slice C). Mode="sync" (the default) is the historical path:
// DeliverOutbound submits to SES inline and returns 200 sent. Mode="async"
// opts into the River pipeline: the accept-tx durably persists the message
// (delivery_status='accepted') + enqueues a send job atomically and returns
// 200 accepted; the internal/outboundsend worker submits to SES and records
// the terminal outcome. Override with E2A_OUTBOUND_MODE. Any value other than
// "async" is treated as "sync" (fail-safe to the unchanged path).
type OutboundConfig struct {
	Mode string `yaml:"mode"`
}

// DeliveryFeedbackConfig controls outbound delivery feedback (decision 9 /
// Slice 4b). When SESConfigurationSet is set, outbound mail is tagged so SES
// publishes delivery/bounce/complaint events; SNSTopicARNs is the fail-closed
// allow-list of SNS topics the public notifications endpoint accepts (empty =
// reject all). Both empty (the default) disables the feature: no event header,
// the endpoint rejects everything. Override with
// E2A_DELIVERY_SES_CONFIGURATION_SET and E2A_DELIVERY_SNS_TOPIC_ARNS (comma-separated).
type DeliveryFeedbackConfig struct {
	SESConfigurationSet string   `yaml:"ses_configuration_set"`
	SNSTopicARNs        []string `yaml:"sns_topic_arns"`
}

// SenderIdentityConfig controls custom-domain sender identity (decision 4 /
// Slice 4). When SESRegion is set (e.g. "us-east-1"), domain verification
// registers an SES BYODKIM sending identity and, once verified, outbound mail
// uses the agent's own address as From. Empty (the default) disables it:
// sending_status stays "none" and outbound uses the relay From — the
// fail-closed default for dev/self-host without SES. Override SESRegion with
// E2A_SENDER_IDENTITY_SES_REGION.
type SenderIdentityConfig struct {
	SESRegion string `yaml:"ses_region"`
}

// LimitsConfig is the operator-configured fallback applied to any user
// who does not yet have a row in account_limits. The hosted billing
// sidecar populates rows for paying customers; self-hosted operators
// who do not run a billing service rely on these defaults for every
// user. Defaults below intentionally lean generous so a self-host that
// never touches the limits subsystem is not accidentally throttled.
//
// Hosted-service operators who want every brand-new signup capped to a
// "free" shape should set these to the Free-tier numbers — the sidecar
// will then overwrite them on upgrade.
type LimitsConfig struct {
	PlanCode         string `yaml:"plan_code"`
	MaxAgents        int    `yaml:"max_agents"`
	MaxDomains       int    `yaml:"max_domains"`
	MaxMessagesMonth int    `yaml:"max_messages_month"`
	MaxStorageBytes  int64  `yaml:"max_storage_bytes"`
	// CacheTTLSeconds controls how long resolved Limits are cached
	// in-process. The cache covers the account_limits read only; current
	// usage counts are always live. Set to 0 to disable caching
	// (recommended for tests that mutate account_limits and want
	// immediate visibility).
	CacheTTLSeconds int `yaml:"cache_ttl_seconds"`
	// InternalAPISecret is the shared HMAC secret the external limits
	// provisioner (e.g. the hosted billing sidecar) uses to authenticate
	// to /api/internal/limits/invalidate. When empty (the self-host
	// default), that endpoint returns 503 — no provisioner, no
	// invalidation. Must be set to the same value on both ends.
	InternalAPISecret string `yaml:"internal_api_secret"`
	// BillingHookURL is the URL the OSS server POSTs to when a user
	// deletes their account, so the external billing service (e.g.
	// the hosted billing sidecar's /api/internal/billing/cancel) can
	// cancel the user's Stripe subscription. Empty disables the call
	// — appropriate for self-host without billing. The same
	// InternalAPISecret signs the POST body.
	BillingHookURL string `yaml:"billing_hook_url"`
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
		// Generous defaults so self-host operators who do not configure
		// `limits:` are not accidentally throttled. Hosted operators
		// override these in config.prod.yaml.
		Limits: LimitsConfig{
			PlanCode:         "default",
			MaxAgents:        1_000_000,
			MaxDomains:       1_000_000,
			MaxMessagesMonth: 1_000_000_000,
			MaxStorageBytes:  1 << 50, // 1 PiB
			CacheTTLSeconds:  60,
		},
		Outbound: OutboundConfig{Mode: "sync"},
		Env:      "development",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Env overrides — secrets only (never duplicated in yaml)
	if v := os.Getenv("E2A_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("E2A_INTERNAL_API_SECRET"); v != "" {
		cfg.Limits.InternalAPISecret = v
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
	if v := os.Getenv("E2A_OAUTH_SIGNING_KEY"); v != "" {
		cfg.OAuth.SigningKey = v
	}
	if v := os.Getenv("E2A_OAUTH_SIGNING_KID"); v != "" {
		cfg.OAuth.SigningKID = v
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
	if v := os.Getenv("E2A_OUTBOUND_MODE"); v != "" {
		cfg.Outbound.Mode = v
	}
	if v := os.Getenv("E2A_OUTBOUND_SMTP_REQUIRE_TLS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.OutboundSMTP.RequireTLS = &b
		}
	}
	if v := os.Getenv("E2A_PUBLIC_URL"); v != "" {
		cfg.HTTP.PublicURL = v
	}
	if v := os.Getenv("E2A_API_URL"); v != "" {
		cfg.HTTP.APIURL = v
	}
	if v := os.Getenv("E2A_SHARED_DOMAIN"); v != "" {
		cfg.SharedDomain = v
	}
	if v := os.Getenv("E2A_SENDER_IDENTITY_SES_REGION"); v != "" {
		cfg.SenderIdentity.SESRegion = v
	}
	if v := os.Getenv("E2A_DELIVERY_SES_CONFIGURATION_SET"); v != "" {
		cfg.DeliveryFeedback.SESConfigurationSet = v
	}
	if v := os.Getenv("E2A_DELIVERY_SNS_TOPIC_ARNS"); v != "" {
		cfg.DeliveryFeedback.SNSTopicARNs = splitAndTrim(v)
	}

	// Default outbound TLS enforcement to on in production when not set
	// explicitly. SES on :587/:465 always advertises STARTTLS, so this is
	// a no-op for the real prod path and only fails closed if the TLS
	// capability disappears (misconfig or active stripping attack).
	if cfg.OutboundSMTP.RequireTLS == nil {
		v := cfg.IsProduction()
		cfg.OutboundSMTP.RequireTLS = &v
	}

	// Default the API URL (OAuth issuer + token/jwks host) to the web
	// PublicURL when unset, so single-host deployments and self-hosters get
	// the historical behaviour (issuer == public_url) with no new config.
	// Split deployments (web on one host, API/MCP on another) set api_url.
	if cfg.HTTP.APIURL == "" {
		cfg.HTTP.APIURL = cfg.HTTP.PublicURL
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
