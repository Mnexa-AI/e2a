package selftest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// roundTripTimeout bounds how long the inbound round-trip waits for the webhook
// callback. It must exceed the SubscriberRetryWorker poll interval in
// production; the prober makes it configurable (see cmd/e2a-prober).
const roundTripTimeout = 30 * time.Second

// All is the critical-path battery. Every scenario here is SmokeSafe: read-only,
// a loopback (no egress), or the inbound round-trip (synthetic mail to the probe
// agent, no real recipient). None meters (the probe runs under a system-class
// account) and none emails an owner.
var All = []Scenario{
	{Name: "liveness", SmokeSafe: true, Run: scenarioLiveness},
	{Name: "auth_read", SmokeSafe: true, Run: scenarioAuthRead},
	{Name: "inbound_round_trip", SmokeSafe: true, Run: scenarioInboundRoundTrip},
	{Name: "self_send_loopback", SmokeSafe: true, Run: scenarioSelfSendLoopback},
}

func pass(detail string) Result { return Result{Status: StatusPass, Detail: detail} }
func fail(format string, a ...any) Result {
	return Result{Status: StatusFail, Detail: fmt.Sprintf(format, a...)}
}

// scenarioLiveness: GET /api/health is up and reports ok. Shallow by design —
// no dependency checks (those belong to /readyz and /selftest).
func scenarioLiveness(ctx context.Context, p *Probe) Result {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.HTTPBaseURL+"/api/health", nil)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return fail("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fail("GET /api/health: HTTP %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("ok")) {
		return fail("GET /api/health: unexpected body %q", string(body))
	}
	return pass("health ok")
}

// scenarioAuthRead: an authenticated read of the probe agent. Exercises API key
// auth + a DB read. The email is percent-encoded — a real client must do this
// (the in-process test client would otherwise hide the encoding bug).
func scenarioAuthRead(ctx context.Context, p *Probe) Result {
	u := p.HTTPBaseURL + "/v1/agents/" + url.PathEscape(p.AgentEmail)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return fail("GET agent: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fail("GET agent: HTTP %d", resp.StatusCode)
	}
	return pass("authenticated read ok")
}

// scenarioInboundRoundTrip is the core check: inject a unique inbound message
// over real SMTP and confirm it comes back out the webhook with a valid HMAC.
// Covers the SMTP listener, emailauth, agent lookup, DB write, outbox, the
// subscriber worker, webhook HTTP delivery, and signing.
func scenarioInboundRoundTrip(ctx context.Context, p *Probe) Result {
	if p.Sink == nil {
		return fail("no sink configured")
	}
	nonce, err := randHex(16)
	if err != nil {
		return fail("nonce: %v", err)
	}
	msg := fmt.Sprintf("From: e2a-selftest <selftest@e2a-selftest.invalid>\r\n"+
		"To: %s\r\n"+
		"Subject: e2a-selftest %s\r\n"+
		"Message-ID: <%s@e2a-selftest.invalid>\r\n"+
		"\r\n"+
		"e2a selftest round-trip %s\r\n", p.AgentEmail, nonce, nonce, nonce)

	if err := smtp.SendMail(p.SMTPAddr, nil, "selftest@e2a-selftest.invalid", []string{p.AgentEmail}, []byte(msg)); err != nil {
		return fail("SMTP send: %v", err)
	}

	d, err := p.Sink.Await(ctx, func(d Delivery) bool {
		return bytes.Contains(d.Body, []byte(nonce))
	}, roundTripTimeout)
	if err != nil {
		return fail("await webhook for nonce %s: %v", nonce, err)
	}
	if !verifyHMAC(d.Headers.Get("X-E2A-Signature"), d.Body, p.WebhookSecret) {
		return fail("webhook HMAC verification failed")
	}
	return pass("inbound round-trip + HMAC ok")
}

// scenarioSelfSendLoopback: the probe agent sends to itself. Self-send routes
// through the loopback path (method=loopback) — no SMTP egress, no HITL owner
// notification — exercising the outbound API + compose path safely.
func scenarioSelfSendLoopback(ctx context.Context, p *Probe) Result {
	u := p.HTTPBaseURL + "/v1/agents/" + url.PathEscape(p.AgentEmail) + "/messages"
	payload := map[string]any{
		"to":      []string{p.AgentEmail},
		"subject": "e2a-selftest loopback",
		"body":    "e2a selftest loopback",
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return fail("self-send: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fail("self-send: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Method != "loopback" {
		return fail("self-send method=%q, want loopback (no egress)", out.Method)
	}
	return pass("self-send loopback ok")
}

// verifyHMAC checks the X-E2A-Signature header against the body using the
// webhook secret. Header format: t=<unix>,v1=<hex>[,v1=<hex>]; signed string is
// "<t>.<body>" (hmac-sha256, hex). Any v1 matching is accepted (rotation grace).
func verifyHMAC(header string, body []byte, secret string) bool {
	if header == "" || secret == "" {
		return false
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "t="):
			ts = strings.TrimPrefix(part, "t=")
		case strings.HasPrefix(part, "v1="):
			sigs = append(sigs, strings.TrimPrefix(part, "v1="))
		}
	}
	if ts == "" || len(sigs) == 0 {
		return false
	}
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.", ts)
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return true
		}
	}
	return false
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
