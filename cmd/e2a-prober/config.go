package main

import (
	"os"
	"time"
)

// config is the prober's runtime configuration, sourced entirely from env so
// the same binary runs in compose, CI, and locally with no flags.
type config struct {
	DatabaseURL   string // seed, validate
	BaseURL       string // run-once, serve
	SMTPAddr      string // run-once, serve
	AgentEmail    string
	APIKey        string
	WebhookSecret string
	MCPBaseURL    string        // deployed streamable-HTTP MCP endpoint; empty ⇒ mcp scenario skips
	SinkURL       string        // what the probe webhook targets (== Listen's public addr + /sink)
	Listen        string        // serve/run-once bind addr for the sink + status server
	Interval      time.Duration // serve loop period
	Timeout       time.Duration // round-trip await timeout
}

func configFromEnv() config {
	return config{
		DatabaseURL:   os.Getenv("E2A_DATABASE_URL"),
		BaseURL:       os.Getenv("E2A_PROBE_BASE_URL"),
		SMTPAddr:      os.Getenv("E2A_PROBE_SMTP_ADDR"),
		AgentEmail:    os.Getenv("E2A_PROBE_AGENT_EMAIL"),
		APIKey:        os.Getenv("E2A_PROBE_API_KEY"),
		WebhookSecret: os.Getenv("E2A_PROBE_WEBHOOK_SECRET"),
		MCPBaseURL:    os.Getenv("E2A_PROBE_MCP_URL"),
		SinkURL:       os.Getenv("E2A_PROBE_SINK_URL"),
		Listen:        envOr("E2A_PROBE_LISTEN", ":8090"),
		Interval:      envDuration("E2A_PROBE_INTERVAL", 30*time.Second),
		Timeout:       envDuration("E2A_PROBE_TIMEOUT", 30*time.Second),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
