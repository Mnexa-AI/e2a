package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
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
		http.StatusBadRequest:          "bad_request",
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
	// Huma's built-in validation errors render in the same shape.
	se := humaErrorConstructor(http.StatusUnprocessableEntity, "validation failed")
	env, ok := se.(*ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *ErrorEnvelope, got %T", se)
	}
	if env.GetStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("status: %d", env.GetStatus())
	}
	if env.Code() != "unprocessable_entity" {
		t.Fatalf("code: %q", env.Code())
	}
}
