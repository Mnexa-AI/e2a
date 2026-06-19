package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// SubscriberDeliverer performs the HTTP POST for a
// webhook_subscriber_deliveries row, signs the request with the
// per-webhook HMAC secret, and reports success / failure to the
// caller. Distinct from the legacy Deliverer (which signs nothing
// at the request level — it forwards X-E2A-Auth-* headers from the
// payload instead).
//
// Slice 1 carries only the current secret. Slice 4 will extend this
// to dual-sign during the 24h rotation grace window.
type SubscriberDeliverer struct {
	client       *http.Client
	requireHTTPS bool
}

// NewSubscriberDeliverer constructs the deliverer with the 15s
// per-attempt timeout chosen in design decision #6.
//
// requireHTTPS gates against plaintext URLs in production. The same flag
// installs a dial-time IP guard (guardedDialControl): registration-time
// ValidateWebhookURL validates DNS once, but a hostname can re-resolve to an
// internal IP before delivery (DNS rebinding). The guard re-checks the actual
// resolved IP at connect time, closing that window. It is gated to production
// so local/CI deliveries to 127.0.0.1 still work.
func NewSubscriberDeliverer(requireHTTPS bool) *SubscriberDeliverer {
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Refuse redirects to prevent SSRF — same defense the
		// legacy Deliverer uses. A registered HTTPS URL that
		// 301s to 127.0.0.1 would otherwise let an attacker
		// reach internal services.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if requireHTTPS {
		client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
				Control:   guardedDialControl,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	return &SubscriberDeliverer{
		client:       client,
		requireHTTPS: requireHTTPS,
	}
}

// DeliveryOutcome is what the deliverer returns to the caller for
// status accounting. statusCode is 0 when there was no HTTP response
// (connection error, timeout, DNS failure).
type DeliveryOutcome struct {
	Success    bool
	StatusCode int
	Error      string
}

// Deliver performs one POST attempt. It signs the request body with
// the supplied HMAC secret in Stripe-style header format:
//
//	X-E2A-Signature: t=<unix>,v1=<hex(hmac-sha256(secret, "<t>.<body>"))>
//
// secretPrev (if non-empty) adds a second v1=... signature for the
// receiver to verify against during the 24h rotation grace window.
// Slice 1 always passes secretPrev="" (no grace logic yet); slice 4
// wires this up.
//
// 2xx responses are success. Anything else (including 3xx, since
// redirects are blocked) is a failure with the HTTP status code
// reported back. Connection errors return Success=false and StatusCode=0.
func (d *SubscriberDeliverer) Deliver(ctx context.Context, url string, body []byte, secret, secretPrev string) DeliveryOutcome {
	if d.requireHTTPS && !strings.HasPrefix(url, "https://") {
		return DeliveryOutcome{Success: false, Error: "webhook URL must use HTTPS in production"}
	}

	timestamp := time.Now().Unix()
	signatureValue := buildSignatureHeader(timestamp, body, secret, secretPrev)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return DeliveryOutcome{Success: false, Error: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-E2A-Signature", signatureValue)
	req.Header.Set("User-Agent", "e2a-webhooks/1")

	resp, err := d.client.Do(req)
	if err != nil {
		return DeliveryOutcome{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return DeliveryOutcome{Success: true, StatusCode: resp.StatusCode}
	}
	return DeliveryOutcome{
		Success:    false,
		StatusCode: resp.StatusCode,
		Error:      fmt.Sprintf("HTTP %d", resp.StatusCode),
	}
}

// buildSignatureHeader formats the X-E2A-Signature header value. The
// header carries one v1= signature in the normal case, and two during
// the rotation grace window (separated by ',') so receivers can verify
// with either secret.
//
// The signed string is "<t>.<body>" — Stripe's exact format. The
// timestamp prevents simple replay (receivers should check that t is
// recent; the design uses a 10-minute tolerance).
func buildSignatureHeader(timestamp int64, body []byte, secret, secretPrev string) string {
	current := signPayload(timestamp, body, secret)
	parts := []string{fmt.Sprintf("t=%d", timestamp), "v1=" + current}
	if secretPrev != "" {
		parts = append(parts, "v1="+signPayload(timestamp, body, secretPrev))
	}
	return strings.Join(parts, ",")
}

// signPayload computes hex(hmac-sha256(secret, "<t>.<body>")).
func signPayload(timestamp int64, body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
