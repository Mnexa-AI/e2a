package agent

import (
	"bytes"
	"log"
	"net/http"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/idempotency"
)

// idempotencyGuard runs the Idempotency-Key handshake at the start of a
// side-effectful POST handler. The transport contract is Stripe-style:
//
//   - No header → transparent no-op. The handler runs unchanged.
//   - Header set + cached completed response (same body) → server writes
//     the cached response verbatim and the caller MUST return from the
//     handler. Detected via the returned `replayed=true`.
//   - Header set + cached completed response (different body) → 422.
//     replayed=true; caller returns.
//   - Header set + another request currently in-flight → 409.
//     replayed=true; caller returns.
//   - Header set + claim acquired → handler proceeds. `out` is a
//     capturing wrapper around w that records the response so the
//     deferred `finalize()` can persist it. `finalize` MUST be deferred
//     by the caller — it decides between Complete and Release based on
//     the observed status code.
//
// On any internal error from the store (e.g. a transient DB blip),
// the guard fails open: the request proceeds without idempotency,
// the operator gets a log line, and no claim is recorded. The
// alternative — failing the request closed — would conflate
// idempotency-store outages with /send outages and make the system
// more brittle than the no-header baseline.
//
// Status-code policy for finalize:
//   - 2xx → Complete (cache for TTL). 202 (HITL held) included; a
//     retry must not create a second pending_approval row.
//   - 4xx or 5xx → Release. Client errors mean the caller can fix
//     and retry; 5xx is treated as "side effect uncertain" — better
//     to risk a double-send than lock the caller out of recovery
//     for the next TTL window. The HITL-approval path (slice 2,
//     send_attempts) layers a separate exactly-once gate for that
//     code path.
func (a *API) idempotencyGuard(w http.ResponseWriter, r *http.Request, userID string, bodyBytes []byte) (replayed bool, out http.ResponseWriter, finalize func()) {
	noop := func() {}

	if a.idempotency == nil {
		return false, w, noop
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return false, w, noop
	}
	if len(key) > idempotency.MaxKeyLength {
		http.Error(w, "Idempotency-Key exceeds max length", http.StatusBadRequest)
		return true, nil, nil
	}

	res, err := a.idempotency.Claim(r.Context(), userID, key, r.URL.Path, idempotency.HashRequest(r.URL.Path, bodyBytes))
	if err != nil {
		log.Printf("[idempotency] claim error: %v (failing open)", err)
		return false, w, noop
	}

	switch res.Outcome {
	case idempotency.OutcomeReplay:
		if res.Cached.ContentType != "" {
			w.Header().Set("Content-Type", res.Cached.ContentType)
		}
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(res.Cached.StatusCode)
		_, _ = w.Write(res.Cached.Body)
		return true, nil, nil

	case idempotency.OutcomeMismatch:
		http.Error(w, "Idempotency-Key reused with a different request body", http.StatusUnprocessableEntity)
		return true, nil, nil

	case idempotency.OutcomeInFlight:
		http.Error(w, "another request with this Idempotency-Key is in progress", http.StatusConflict)
		return true, nil, nil

	case idempotency.OutcomeAcquired:
		cap := &capturingWriter{ResponseWriter: w}
		return false, cap, func() {
			// WriteHeader may not have been called explicitly (Go's
			// http package implicitly writes 200 on first Write).
			code := cap.statusCode
			if code == 0 {
				code = http.StatusOK
			}
			if code >= 400 {
				if err := a.idempotency.Release(r.Context(), userID, key); err != nil {
					log.Printf("[idempotency] release error: %v", err)
				}
				return
			}
			if err := a.idempotency.Complete(r.Context(), userID, key, idempotency.CachedResponse{
				StatusCode:  code,
				ContentType: cap.Header().Get("Content-Type"),
				Body:        cap.body.Bytes(),
			}); err != nil {
				log.Printf("[idempotency] complete error: %v", err)
			}
		}
	}

	// Unreachable in practice; satisfy the compiler with a no-op pass-through.
	return false, w, noop
}

// capturingWriter is a minimal http.ResponseWriter wrapper that
// records the outgoing status code and body so the idempotency layer
// can cache the response for replay. It still forwards everything to
// the underlying writer, so the live client receives the response
// unchanged.
//
// Not safe for concurrent use; handlers write their response from a
// single goroutine, matching the http server contract.
type capturingWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (c *capturingWriter) WriteHeader(code int) {
	if c.statusCode == 0 {
		c.statusCode = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingWriter) Write(b []byte) (int, error) {
	if c.statusCode == 0 {
		c.statusCode = http.StatusOK
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
