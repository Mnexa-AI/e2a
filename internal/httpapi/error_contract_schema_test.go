package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	agentapi "github.com/Mnexa-AI/e2a/internal/agent"
)

func TestErrorBodyOpenEnvelopeSchema(t *testing.T) {
	raw, err := json.Marshal(New(Deps{}).API.OpenAPI())
	if err != nil {
		t.Fatalf("render OpenAPI: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	body := doc.Components.Schemas["ErrorBody"]
	if body == nil {
		t.Fatal("ErrorBody component is missing")
	}
	if got, want := stringsFromAny(t, body["required"]), []string{"code", "message", "request_id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ErrorBody required = %v, want %v", got, want)
	}
	properties := body["properties"].(map[string]any)
	details := properties["details"].(map[string]any)
	if details["type"] != "object" || details["additionalProperties"] != true {
		t.Errorf("ErrorBody.details must be an open object, got %#v", details)
	}
	if _, ok := details["anyOf"]; ok {
		t.Error("ErrorBody.details must not use anyOf")
	}
	if _, ok := details["oneOf"]; ok {
		t.Error("ErrorBody.details must not use oneOf")
	}
	if _, ok := properties["code"].(map[string]any)["x-e2a-error-contracts"]; !ok {
		t.Error("ErrorBody.code is missing x-e2a-error-contracts")
	}
	if _, ok := details["x-e2a-error-details-schemas"]; !ok {
		t.Error("ErrorBody.details is missing x-e2a-error-details-schemas")
	}
}

func TestOpenAPIErrorExtensionsMatchCatalog(t *testing.T) {
	raw, err := json.Marshal(New(Deps{}).API.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	body := schemas["ErrorBody"].(map[string]any)
	properties := body["properties"].(map[string]any)
	contracts := properties["code"].(map[string]any)["x-e2a-error-contracts"].(map[string]any)
	mapping := properties["details"].(map[string]any)["x-e2a-error-details-schemas"].(map[string]any)
	if len(contracts) != len(errorCodeCatalog) {
		t.Fatalf("extension has %d codes, catalog has %d", len(contracts), len(errorCodeCatalog))
	}
	for _, entry := range errorCodeCatalog {
		metadata, ok := contracts[entry.Code].(map[string]any)
		if !ok {
			t.Errorf("missing contract for %s", entry.Code)
			continue
		}
		if metadata["family"] != entry.Family || metadata["retryable"] != entry.Retryable {
			t.Errorf("%s metadata = %#v", entry.Code, metadata)
		}
		if entry.DetailsSchema != "" {
			want := "#/components/schemas/" + entry.DetailsSchema
			if mapping[entry.Code] != want || metadata["details_schema"] != want {
				t.Errorf("%s details mapping = %#v metadata=%#v, want %s", entry.Code, mapping[entry.Code], metadata["details_schema"], want)
			}
		}
	}
}

func TestErrorRequestIDAlwaysSerializes(t *testing.T) {
	raw, err := json.Marshal(NewError(400, "invalid_request", "bad request"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if value, ok := decoded["error"]["request_id"]; !ok || value != "" {
		t.Fatalf("request_id = %#v, present=%v; want required empty string before transport stamping", value, ok)
	}
}

func TestManualInvalidRequestGetsValidationDetails(t *testing.T) {
	env := NewError(400, "invalid_request", "request-wide failure")
	details, ok := env.Err.Details.(ValidationErrorDetails)
	if !ok {
		t.Fatalf("details = %T, want ValidationErrorDetails", env.Err.Details)
	}
	if len(details.Fields) != 1 || details.Fields[0].Location != "" || details.Fields[0].Message != "request-wide failure" {
		t.Fatalf("fields = %#v", details.Fields)
	}
}

func TestStableErrorDetailFixturesValidateAgainstEnvelopeAndMapping(t *testing.T) {
	server := New(Deps{})
	registry := server.API.OpenAPI().Components.Schemas
	envelope := registry.Map()["ErrorEnvelope"]
	if envelope == nil {
		t.Fatal("ErrorEnvelope component is missing")
	}
	fixtures := map[string]string{
		"invalid_request":     "ValidationErrorDetails",
		"too_many_recipients": "TooManyRecipientsDetails",
		"payload_too_large":   "PayloadTooLargeDetails",
		"limit_exceeded":      "LimitExceededDetails",
		"rate_limited":        "RateLimitedDetails",
		"limits_unavailable":  "RetryAfterDetails",
	}
	for code, schemaName := range fixtures {
		code, schemaName := code, schemaName
		t.Run(code, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "api", "fixtures", "errors", code+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			validateSchema(t, registry, envelope, "response", decoded)
			errorBody := decoded["error"].(map[string]any)
			if errorBody["code"] != code {
				t.Fatalf("code = %#v, want %q", errorBody["code"], code)
			}
			validateSchema(t, registry, registry.Map()[schemaName], "details", errorBody["details"])
		})
	}
}

func TestOutboundErrorDetailsSurviveHTTPBoundary(t *testing.T) {
	details := map[string]any{
		"scope":        "composed_message",
		"actual_bytes": outboundMaxFixtureBytes + 1,
		"max_bytes":    outboundMaxFixtureBytes,
	}
	env := envelopeFromOutboundError(&agentapi.OutboundError{
		Status:  413,
		Code:    "payload_too_large",
		Msg:     "too large",
		Details: details,
	})
	if !reflect.DeepEqual(env.Err.Details, details) {
		t.Fatalf("details = %#v, want %#v", env.Err.Details, details)
	}
}

func TestRequestBodyTooLargeGetsNormalizedDetails(t *testing.T) {
	const maxBodyBytes = 1024 * 1024
	body := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	New(Deps{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Error struct {
			Details PayloadTooLargeDetails `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if got := envelope.Error.Details; got.Scope != "request_body" || got.ActualBytes != int64(len(body)) || got.MaxBytes != maxBodyBytes {
		t.Fatalf("details = %#v", got)
	}
}

const outboundMaxFixtureBytes = 10 * 1024 * 1024
