// Package httpapi is the e2a v1 HTTP contract layer, built on Huma + chi.
//
// It exists to make the OpenAPI 3.1 spec the single source of truth: every
// operation is declared with typed Go input/output structs, and Huma emits
// the spec *and* validates requests from those same definitions, so the
// handler is the contract and the spec cannot drift by construction
// (api-v1-redesign §6). This package is the foundation slice (Slice 1):
// the canonical error envelope, cursor pagination, idempotency, and shared
// middleware that every ported operation reuses.
//
// chi owns the `/v1` prefix and falls back to the legacy gorilla/mux for the
// remaining non-v1 routes (OAuth, session auth, health/feedback, the magic-link
// approve/reject pages). The `/api/v1` surface this strangler replaced is fully
// retired — no `/api/v1` route is registered anymore.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// ErrorEnvelope is the one error shape across every v1 endpoint
// (api-v1-redesign §4 decision 6):
//
//	{ "error": { "code": "machine_branchable", "message": "human text",
//	             "details": {…}, "request_id": "req_…" } }
//
// `code` is the stable, machine-branchable discriminator agents switch on;
// `message` is human-facing; `details` is optional structured context; and
// `request_id` echoes the per-request id (also on the X-Request-Id header)
// so a failing call is greppable in logs without correlation guesswork.
//
// It implements huma.StatusError so it can be returned directly from a
// handler and is installed as the global huma.NewError constructor, which
// means Huma's own validation/automatic errors render in this envelope too.
type ErrorEnvelope struct {
	// status is the HTTP status; unexported so it never serializes into
	// the body (the status already rides the status line).
	status int

	// retryAfter, when > 0, is the seconds value stampRequestID copies into the
	// Retry-After response header. A StatusError returned from a handler renders
	// status + body only, so a 429 raised inside a handler (the per-agent send
	// limiter) carries its Retry-After here; the middleware limiter path sets
	// the header directly instead. Unexported so it never serializes.
	retryAfter int

	Err ErrorBody `json:"error"`
}

// ErrorBody is the inner object of the envelope.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// LimitExceededDetails is the typed `error.details` payload carried by a 402
// limit_exceeded response. `resource` is one of the AccountView usage/limits
// field stems, so a client can key the error straight to the usage/cap field:
// usage.<resource> for the current value and limits.max_<resource> for the cap
// (e.g. resource "messages_month" → usage.messages_month / limits.max_messages_month).
// `limit` and `current` echo the cap that was hit and the account's usage at the
// time. `plan_code`/`upgrade_url` are the account's plan label and any upgrade
// affordance the operator configured.
type LimitExceededDetails struct {
	Resource   string `json:"resource" enum:"agents,domains,messages_month,storage_bytes" doc:"The AccountView usage/limits field stem the cap applies to. Key it to usage.<resource> and limits.max_<resource>."`
	Limit      int64  `json:"limit" doc:"The cap that was hit (matches limits.max_<resource>)."`
	Current    int64  `json:"current" doc:"The account's usage at the time the cap was hit (matches usage.<resource>)."`
	PlanCode   string `json:"plan_code,omitempty" doc:"The account's plan label."`
	UpgradeURL string `json:"upgrade_url,omitempty" doc:"An upgrade affordance URL, when the operator has configured one."`
}

// LimitExceededErrorBody mirrors ErrorBody but with typed limit_exceeded details,
// so codegen surfaces a concrete detail shape for the 402 case instead of `any`.
type LimitExceededErrorBody struct {
	Code      string               `json:"code" enum:"limit_exceeded" doc:"Always limit_exceeded for this response."`
	Message   string               `json:"message"`
	Details   LimitExceededDetails `json:"details"`
	RequestID string               `json:"request_id,omitempty"`
}

// LimitExceededEnvelope is the 402 error envelope with typed details. It is the
// declared schema for the 402 response on the cap-enforcing operations (create
// agent, register domain, send/reply/forward/test); the runtime envelope is the
// generic ErrorEnvelope whose `details` is populated with a LimitExceededDetails
// value, so the wire shape matches this schema byte-for-byte.
type LimitExceededEnvelope struct {
	Err LimitExceededErrorBody `json:"error"`
}

// Error implements the error interface (huma.StatusError embeds error).
func (e *ErrorEnvelope) Error() string { return e.Err.Message }

// GetStatus implements huma.StatusError so Huma writes the right status.
func (e *ErrorEnvelope) GetStatus() int { return e.status }

// Code returns the machine-branchable code (used by tests and middleware).
func (e *ErrorEnvelope) Code() string { return e.Err.Code }

// NewError builds an envelope with an explicit machine-branchable code.
// Prefer this over the status-only helpers when the caller should be able
// to branch on something more specific than the HTTP status (e.g.
// "domain_not_verified" vs a bare 400).
func NewError(status int, code, message string) *ErrorEnvelope {
	return &ErrorEnvelope{status: status, Err: ErrorBody{Code: code, Message: message}}
}

// WithDetails attaches structured details and returns the envelope for
// fluent construction.
func (e *ErrorEnvelope) WithDetails(details any) *ErrorEnvelope {
	e.Err.Details = details
	return e
}

// WithRetryAfter records a Retry-After delay (seconds) for a handler-returned
// error; stampRequestID copies it into the Retry-After response header. Use it
// on 429s raised inside a handler (the per-agent send limiter) — the
// middleware-enforced limiters set the header themselves.
func (e *ErrorEnvelope) WithRetryAfter(seconds int) *ErrorEnvelope {
	e.retryAfter = seconds
	return e
}

// defaultCodeForStatus maps an HTTP status to a stable default `code` for
// the cases where a handler (or Huma's built-in validation) produced only a
// status + message. Ported handlers should pass an explicit code via
// NewError; this is the fallback so every error still carries a non-empty,
// branchable code.
func defaultCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error"
	}
}

// humaErrorConstructor is installed as huma.NewError so that *all* errors —
// handler-returned, body-validation, content-negotiation — render in the
// e2a envelope. Huma passes the status, a message, and zero or more detail
// errors; we fold the detail errors into `details` so field-level
// validation failures survive.
func humaErrorConstructor(status int, message string, errs ...error) huma.StatusError {
	env := &ErrorEnvelope{
		status: status,
		Err: ErrorBody{
			Code:    defaultCodeForStatus(status),
			Message: message,
		},
	}
	if len(errs) > 0 {
		// huma.ErrorDetailer values carry structured field/location info;
		// preserve them as-is so validation output stays machine-readable.
		details := make([]any, 0, len(errs))
		for _, err := range errs {
			if err == nil {
				continue
			}
			if d, ok := err.(huma.ErrorDetailer); ok {
				details = append(details, d.ErrorDetail())
			} else {
				details = append(details, map[string]string{"message": err.Error()})
			}
		}
		if len(details) > 0 {
			env.Err.Details = details
		}
	}
	return env
}

// stampRequestID is a Huma transformer that copies the per-request id into
// the error envelope body just before serialization, so the body matches
// the X-Request-Id header (api-v1-redesign §4 — "echo the same id in the
// error envelope"). Success bodies are left untouched.
func stampRequestID(ctx huma.Context, status string, v any) (any, error) {
	env, ok := v.(*ErrorEnvelope)
	if !ok {
		return v, nil
	}
	if env.Err.RequestID == "" {
		env.Err.RequestID = RequestIDFromContext(ctx.Context())
	}
	// A StatusError returned from a handler renders status + body only, so stamp
	// the Retry-After header here for rate-limit errors that carry a delay —
	// matching the middleware limiter path, which sets the header itself.
	if env.retryAfter > 0 {
		ctx.SetHeader("Retry-After", strconv.Itoa(env.retryAfter))
	}
	return v, nil
}

// writeRawEnvelope serializes an ErrorEnvelope to a raw (non-Huma)
// ResponseWriter, giving handlers that bypass Huma the SAME error contract every
// operation emits. It reuses the request id the requestID middleware already
// stamped (the production chi root always sets one, so header == body == what
// REST would return) and mints one only when absent (a direct call in a test),
// then mirrors it onto the X-Request-Id header and sets Content-Type:
// application/json before writing the status + body. This is the one place raw
// chi routes stay in lockstep with the Huma surface on the envelope shape.
// (The middleware path uses the huma.Context-based writeEnvelope in ratelimit.go.)
func writeRawEnvelope(w http.ResponseWriter, r *http.Request, env *ErrorEnvelope) {
	id := RequestIDFromContext(r.Context())
	if id == "" {
		id = newRequestID()
	}
	env.Err.RequestID = id
	w.Header().Set(requestIDHeader, id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(env.status)
	_ = json.NewEncoder(w).Encode(env)
}

// WriteError writes the canonical v1 error envelope to a raw ResponseWriter for
// handlers OUTSIDE this package that bypass Huma — specifically the WebSocket
// upgrade handshake (internal/ws), which authenticates and authorizes BEFORE the
// upgrade and so rejects a bad handshake with a normal HTTP response. Routing
// those rejections through here makes the WS handshake body byte-for-byte
// consistent with every /v1 REST endpoint: {error:{code,message,request_id}} +
// X-Request-Id. The caller supplies the status and a code from the REST
// vocabulary (unauthorized / forbidden / not_found / bad_request); status codes
// are the caller's to choose so this never rewrites them.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeRawEnvelope(w, r, NewError(status, code, message))
}

// installErrorEnvelope wires the envelope constructor globally. It is called
// once from New(); calling it is idempotent.
func installErrorEnvelope() {
	huma.NewError = humaErrorConstructor
}
