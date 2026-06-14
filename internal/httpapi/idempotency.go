package httpapi

import (
	"context"
	"encoding/json"

	"github.com/Mnexa-AI/e2a/internal/idempotency"
)

// IdemStore is the subset of *idempotency.Store the v1 layer needs. Declared
// as an interface so handlers are unit-testable without Postgres.
type IdemStore interface {
	Claim(ctx context.Context, userID, key, path, bodyHash string) (idempotency.ClaimResult, error)
	Complete(ctx context.Context, userID, key string, resp idempotency.CachedResponse) error
	Release(ctx context.Context, userID, key string) error
}

// runIdempotent executes fn under the `Idempotency-Key` handshake and returns
// the (status, body) to emit. It is the one place the v1 write endpoints
// (send/reply/forward/redeliver) get retry-safety, replacing the legacy
// capturingWriter (which doesn't fit Huma's return-value handler model).
//
// Semantics (api-v1-redesign §4 decision 8):
//   - No key, or no store wired → just run fn (idempotency is opt-in).
//   - Dedup key = (principal, route, body-hash). The body hash is
//     load-bearing: the same key with a *different* body is a 422, never a
//     silent replay of the first response. This matters most on the unified
//     outbound route where send/reply/forward share one path.
//   - Replay → the cached response, byte-faithful (unmarshaled back into T).
//   - In-flight → 409; mismatch → 422.
//
// fn's contract: return a non-nil error ONLY before any irreversible side
// effect (so the key is released and a retry can proceed); once the side
// effect commits, return success with the final response so it is cached and
// a retry replays it instead of re-doing the side effect.
func runIdempotent[T any](s *Server, ctx context.Context, userID, key, route string, rawBody []byte, fn func() (int, T, error)) (int, T, error) {
	var zero T
	if key == "" || s.deps.Idempotency == nil {
		return fn()
	}
	hash := idempotency.HashRequest(route, rawBody)
	claim, err := s.deps.Idempotency.Claim(ctx, userID, key, route, hash)
	if err != nil {
		return 0, zero, NewError(500, "internal_error", "idempotency store error")
	}
	switch claim.Outcome {
	case idempotency.OutcomeReplay:
		var body T
		if e := json.Unmarshal(claim.Cached.Body, &body); e != nil {
			return 0, zero, NewError(500, "internal_error", "failed to decode cached response")
		}
		return claim.Cached.StatusCode, body, nil
	case idempotency.OutcomeInFlight:
		return 0, zero, NewError(409, "idempotency_in_flight", "a request with this Idempotency-Key is already in progress")
	case idempotency.OutcomeMismatch:
		return 0, zero, NewError(422, "idempotency_key_reuse", "Idempotency-Key was reused with a different request body")
	}
	// OutcomeAcquired: we own the key and must Complete (success) or
	// Release (pre-side-effect failure).
	status, body, ferr := fn()
	if ferr != nil {
		_ = s.deps.Idempotency.Release(ctx, userID, key)
		return 0, zero, ferr
	}
	// The side effect has committed. We MUST Complete the key — never leave
	// it Acquired (orphaned) — so a retry replays the cached response rather
	// than re-doing the side effect (e.g. re-sending email). Releasing here
	// would be the wrong move: it would let a retry re-execute. If the body
	// somehow fails to marshal, cache an empty body (status preserved) — a
	// replayed empty body is far better than a double-send.
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		raw = []byte("{}")
	}
	_ = s.deps.Idempotency.Complete(ctx, userID, key, idempotency.CachedResponse{
		StatusCode: status, ContentType: "application/json", Body: raw,
	})
	return status, body, nil
}
