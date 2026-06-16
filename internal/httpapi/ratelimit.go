package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// RateSnapshot records an attempt for key and reports whether it is allowed
// along with the IETF RateLimit header values (quota, remaining-after-this,
// seconds-until-reset) and the Retry-After delay when blocked. It mirrors
// ratelimit.Limiter.AllowSnapshot so httpapi shares the exact buckets the
// legacy surface uses without importing the limiter directly.
type RateSnapshot func(key string) (ok bool, retryAfter time.Duration, limit, remaining, resetSeconds int)

// pollLimitedOps are the authenticated read operations governed by the
// per-user poll limiter. This mirrors EXACTLY the set the legacy gorilla/mux
// surface poll-limited (verified against origin/main: handleGetMessages,
// handleGetMessage, handleListConversations, handleGetConversation,
// handleListWebhooks, handleGetWebhook, handleListWebhookDeliveries — the
// label PATCH stays legacy-only). The legacy surface deliberately did NOT
// poll-limit agents/domains/events/limits/export reads, so neither do we:
// notably the events API is built for reconciliation polling and must not
// compete for the shared 60/min message-read budget. getInfo is public (no
// principal to key on).
var pollLimitedOps = map[string]bool{
	"listMessages": true, "getMessage": true,
	"listConversations": true, "getConversation": true,
	"listWebhooks": true, "getWebhook": true, "listWebhookDeliveries": true,
}

// rateLimit is the Huma middleware that enforces the per-user poll limiter on
// reads and the per-IP registration limiter on agent create, and stamps the
// IETF RateLimit-Limit/Remaining/Reset headers (plus Retry-After on a 429) on
// the response. The per-agent SEND limiter is enforced inside the outbound
// handlers instead: its key is the *resolved owned* agent (after the
// resolveOwnedAgent ownership check), which this middleware doesn't perform —
// so the send limit is applied in deliver()/the outbound handlers, not here.
func (s *Server) rateLimit(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	if op == nil {
		next(ctx)
		return
	}

	var snap RateSnapshot
	var key string
	switch {
	case pollLimitedOps[op.OperationID] && s.deps.PollLimit != nil:
		r := RequestFromContext(ctx.Context())
		if r == nil || s.deps.Authenticator == nil {
			next(ctx)
			return
		}
		p, err := s.resolvePrincipal(r)
		if err != nil {
			// Unauthenticated: let the handler emit the canonical 401 rather
			// than masking a missing credential as a rate-limit decision.
			next(ctx)
			return
		}
		snap, key = s.deps.PollLimit, p.User.ID
		// Reuse the principal so the handler does not authenticate a second
		// time on the hot read path.
		ctx = huma.WithContext(ctx, withPrincipal(ctx.Context(), p))
	case op.OperationID == "createAgent" && s.deps.RegLimit != nil:
		r := RequestFromContext(ctx.Context())
		if r == nil {
			next(ctx)
			return
		}
		snap, key = s.deps.RegLimit, clientIP(r)
	default:
		next(ctx)
		return
	}

	ok, retryAfter, limit, remaining, reset := snap(key)
	ctx.SetHeader("RateLimit-Limit", strconv.Itoa(limit))
	ctx.SetHeader("RateLimit-Remaining", strconv.Itoa(remaining))
	ctx.SetHeader("RateLimit-Reset", strconv.Itoa(reset))
	if ok {
		next(ctx)
		return
	}
	secs := int(retryAfter.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	ctx.SetHeader("Retry-After", strconv.Itoa(secs))
	writeEnvelope(ctx, NewError(http.StatusTooManyRequests, "rate_limited",
		"rate limit exceeded").WithDetails(map[string]any{"retry_after_seconds": secs}))
}

// writeEnvelope serializes an error envelope directly to the response from a
// middleware, where there is no handler return value for Huma to render. It
// stamps the request id so the body matches the stampRequestID transformer on
// the handler path, and sets headers before the status line.
func writeEnvelope(ctx huma.Context, env *ErrorEnvelope) {
	env.Err.RequestID = RequestIDFromContext(ctx.Context())
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(env.status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(env)
}

// principalCtxKey carries a principal resolved by the rate-limit middleware so
// the downstream handler's requireUser/requirePrincipal can skip a second auth.
type principalCtxKey struct{}

func withPrincipal(ctx context.Context, p *identity.Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

func principalFromContext(ctx context.Context) *identity.Principal {
	if p, ok := ctx.Value(principalCtxKey{}).(*identity.Principal); ok {
		return p
	}
	return nil
}

func userFromContext(ctx context.Context) *identity.User {
	if p := principalFromContext(ctx); p != nil {
		return p.User
	}
	return nil
}

// clientIP extracts the caller IP for per-IP limiting, honoring a single
// X-Forwarded-For hop (mirrors agent.clientIP so both surfaces key the
// registration limiter identically).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
