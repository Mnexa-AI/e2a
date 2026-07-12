package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestNewErrorEnvelopeShape(t *testing.T) {
	e := NewError(http.StatusBadRequest, "domain_not_verified", "verify your domain first").
		WithDetails(map[string]string{"domain": "acme.com"})

	if e.GetStatus() != http.StatusBadRequest {
		t.Fatalf("status: got %d", e.GetStatus())
	}
	if e.Code() != "domain_not_verified" {
		t.Fatalf("code: got %q", e.Code())
	}
	raw, _ := json.Marshal(e)
	var decoded struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Error.Code != "domain_not_verified" ||
		decoded.Error.Message != "verify your domain first" ||
		decoded.Error.Details["domain"] != "acme.com" {
		t.Fatalf("unexpected envelope: %s", raw)
	}
}

func TestDefaultCodeForStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:          "invalid_request",
		http.StatusUnauthorized:        "unauthorized",
		http.StatusForbidden:           "forbidden",
		http.StatusNotFound:            "not_found",
		http.StatusConflict:            "conflict",
		http.StatusTooManyRequests:     "rate_limited",
		http.StatusInternalServerError: "internal_error",
		http.StatusBadGateway:          "internal_error",
		418:                            "error",
	}
	for status, want := range cases {
		if got := defaultCodeForStatus(status); got != want {
			t.Errorf("status %d: got %q want %q", status, got, want)
		}
	}
}

func TestHumaErrorConstructorEnvelope(t *testing.T) {
	// The constructor installed as huma.NewError must yield our envelope so
	// Huma's built-in validation errors render in the same shape. A 422 semantic
	// validation failure now carries the single canonical validation code.
	se := humaErrorConstructor(http.StatusUnprocessableEntity, "validation failed")
	env, ok := se.(*ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *ErrorEnvelope, got %T", se)
	}
	if env.GetStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("status: %d", env.GetStatus())
	}
	if env.Code() != "invalid_request" {
		t.Fatalf("code: %q", env.Code())
	}
}

// TestHumaErrorConstructorTypedDetails asserts that field-level validation
// errors fold into the typed ValidationErrorDetails shape ({fields:[{location,
// message}]}) and that the raw offending value is dropped from the envelope.
func TestHumaErrorConstructorTypedDetails(t *testing.T) {
	detail := &huma.ErrorDetail{
		Message:  "expected string",
		Location: "body.events",
		Value:    "should-not-leak",
	}
	se := humaErrorConstructor(http.StatusUnprocessableEntity, "validation failed", detail)
	env := se.(*ErrorEnvelope)

	vd, ok := env.Err.Details.(ValidationErrorDetails)
	if !ok {
		t.Fatalf("details type: got %T, want ValidationErrorDetails", env.Err.Details)
	}
	if len(vd.Fields) != 1 {
		t.Fatalf("fields: got %d, want 1", len(vd.Fields))
	}
	if vd.Fields[0].Location != "body.events" || vd.Fields[0].Message != "expected string" {
		t.Fatalf("field: got %+v", vd.Fields[0])
	}
	// The raw value must never survive into the public envelope.
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), "should-not-leak") {
		t.Fatalf("raw value leaked into envelope: %s", raw)
	}
}
