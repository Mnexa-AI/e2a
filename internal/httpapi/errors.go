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
	"net/http"

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

	Err ErrorBody `json:"error"`
}

// ErrorBody is the inner object of the envelope.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id,omitempty"`
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
	if env, ok := v.(*ErrorEnvelope); ok && env.Err.RequestID == "" {
		env.Err.RequestID = RequestIDFromContext(ctx.Context())
	}
	return v, nil
}

// installErrorEnvelope wires the envelope constructor globally. It is called
// once from New(); calling it is idempotent.
func installErrorEnvelope() {
	huma.NewError = humaErrorConstructor
}
