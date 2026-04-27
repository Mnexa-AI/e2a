package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

type Payload struct {
	MessageID      string            `json:"message_id,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
	From           string            `json:"from"`
	// To is the parsed To: header from the inbound message — every fan-out
	// delivery for one inbound message carries the same list. Recipient is
	// this delivery's per-agent target (always one of the addressed agents,
	// not necessarily in To: when the agent was Bcc'd).
	To          []string          `json:"to"`
	CC          []string          `json:"cc,omitempty"`
	Recipient   string            `json:"recipient"`
	RawMessage  []byte            `json:"raw_message"`
	AuthHeaders map[string]string `json:"auth_headers"`
	ReceivedAt  time.Time         `json:"received_at"`
}

type Deliverer struct {
	client       *http.Client
	requireHTTPS bool
}

func NewDeliverer(requireHTTPS bool) *Deliverer {
	return &Deliverer{
		client: &http.Client{
			Timeout: 30 * time.Second,
			// Refuse redirects entirely. The webhook URL is validated at
			// agent registration time (public IP, HTTPS in production); a
			// redirect lets the registered host point delivery at an
			// internal address (SSRF) — e.g. attacker.com 301 → 127.0.0.1.
			// Webhooks are agent-controlled endpoints; if they need
			// redirection they can do it themselves.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		requireHTTPS: requireHTTPS,
	}
}

// Deliver is a convenience alias for DeliverHTTP.
func (d *Deliverer) Deliver(ctx context.Context, agent *identity.AgentIdentity, p Payload) error {
	return d.DeliverHTTP(ctx, agent, p)
}

// DeliverHTTP performs the actual HTTP POST to the agent's webhook URL.
func (d *Deliverer) DeliverHTTP(ctx context.Context, agent *identity.AgentIdentity, p Payload) error {
	if d.requireHTTPS && !strings.HasPrefix(agent.WebhookURL, "https://") {
		return fmt.Errorf("webhook URL must use HTTPS in production")
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agent.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Forward auth headers
	for k, v := range p.AuthHeaders {
		req.Header.Set(k, v)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[webhook] POST %s → %d (%d bytes)", agent.WebhookURL, resp.StatusCode, len(body))

	// Treat redirect responses (300-399) as failures. With CheckRedirect
	// returning ErrUseLastResponse we never follow them; surfacing the
	// status as an error keeps the operator from being confused by a
	// successful-looking 302.
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}
