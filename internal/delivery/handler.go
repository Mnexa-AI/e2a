package delivery

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

// maxSNSBody caps the SNS POST body. SES notifications are small; this guards
// against a junk flood on the public endpoint.
const maxSNSBody = 256 * 1024

// Handler returns the HTTP handler for the public SES-over-SNS notifications
// endpoint. Every request is fail-closed: the SNS signature is verified before
// anything is acted on (the endpoint is public). It auto-confirms a
// SubscriptionConfirmation (GET the allow-listed SubscribeURL) and feeds a
// Notification's SES event to the Consumer.
//
// Responses: 403 on a failed signature; 400 on unparseable JSON; 200 on a
// handled or safely-ignored message (so SES stops retrying); 500 only when the
// Consumer hits a real error worth a retry.
func Handler(v *Verifier, c *Consumer) http.HandlerFunc {
	// No redirect-following: the SubscribeURL host is allow-listed
	// (sns.*.amazonaws.com) before the GET, and we must not let a redirect carry
	// the request to a non-allow-listed (internal) host — SSRF defense in depth.
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSNSBody))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		var m SNSMessage
		if err := json.Unmarshal(body, &m); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// Fail-closed: verify the SNS signature before acting on anything.
		if err := v.Verify(r.Context(), &m); err != nil {
			log.Printf("[delivery] SNS signature verification failed: %v", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		switch m.Type {
		case "SubscriptionConfirmation":
			if url, ok := ConfirmSubscriptionURL(&m); ok {
				if err := confirmSubscription(r.Context(), client, url); err != nil {
					log.Printf("[delivery] subscription confirm failed: %v", err)
				} else {
					log.Printf("[delivery] confirmed SNS subscription for topic %s", m.TopicArn)
				}
			}
			w.WriteHeader(http.StatusOK)
		case "Notification":
			ev, err := ParseSESNotification([]byte(m.Message))
			if err != nil {
				// Malformed/unactionable SES payload — ack so SES stops retrying
				// (retrying won't fix bad data); log for visibility.
				log.Printf("[delivery] parse SES notification: %v", err)
				w.WriteHeader(http.StatusOK)
				return
			}
			if err := c.Process(r.Context(), ev); err != nil {
				log.Printf("[delivery] process %s: %v", ev.Kind, err)
				http.Error(w, "processing error", http.StatusInternalServerError) // SES retries
				return
			}
			w.WriteHeader(http.StatusOK)
		default: // UnsubscribeConfirmation, etc.
			w.WriteHeader(http.StatusOK)
		}
	}
}

// confirmSubscription GETs the (already host-allow-listed) SubscribeURL to
// confirm the SNS subscription.
func confirmSubscription(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{resp.StatusCode}
	}
	return nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return "confirm subscription: non-2xx status" }
