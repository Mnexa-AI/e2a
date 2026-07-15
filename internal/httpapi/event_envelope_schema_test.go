package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/danielgtaylor/huma/v2"
)

func TestEventEnvelopeIsOpenAndMapped(t *testing.T) {
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
	envelope, ok := doc.Components.Schemas["EventEnvelope"]
	if !ok {
		t.Fatal("EventEnvelope component is missing")
	}
	if got := envelope["additionalProperties"]; got != true {
		t.Errorf("EventEnvelope additionalProperties = %#v, want true", got)
	}
	if _, ok := envelope["oneOf"]; ok {
		t.Error("EventEnvelope must not contain oneOf")
	}
	if _, ok := envelope["anyOf"]; ok {
		t.Error("EventEnvelope must not contain anyOf")
	}
	if _, ok := envelope["discriminator"]; ok {
		t.Error("EventEnvelope must not contain a discriminator")
	}

	wantRequired := []string{"type", "id", "schema_version", "created_at", "data"}
	gotRequired := stringsFromAny(t, envelope["required"])
	if !reflect.DeepEqual(gotRequired, wantRequired) {
		t.Errorf("EventEnvelope required = %v, want %v", gotRequired, wantRequired)
	}

	properties, ok := envelope["properties"].(map[string]any)
	if !ok {
		t.Fatalf("EventEnvelope properties = %#v", envelope["properties"])
	}
	for _, name := range []string{"type", "schema_version"} {
		property := properties[name].(map[string]any)
		if property["type"] != "string" {
			t.Errorf("EventEnvelope.%s type = %#v, want string", name, property["type"])
		}
		if _, ok := property["enum"]; ok {
			t.Errorf("EventEnvelope.%s must remain an open string", name)
		}
	}

	data, ok := properties["data"].(map[string]any)
	if !ok {
		t.Fatalf("EventEnvelope.data = %#v", properties["data"])
	}
	if data["type"] != "object" || data["additionalProperties"] != true {
		t.Errorf("EventEnvelope.data must be an open object, got %#v", data)
	}
	mapping, ok := data["x-e2a-event-data-schemas"].(map[string]any)
	if !ok {
		t.Fatalf("EventEnvelope.data mapping = %#v", data["x-e2a-event-data-schemas"])
	}
	if len(mapping) != len(eventpayload.StableEvents) {
		t.Errorf("event mapping has %d entries, want %d", len(mapping), len(eventpayload.StableEvents))
	}
	for _, event := range eventpayload.StableEvents {
		want := "#/components/schemas/" + event.SchemaName
		if got := mapping[event.Type]; got != want {
			t.Errorf("mapping[%s] = %#v, want %q", event.Type, got, want)
		}
	}
}

func TestStableEventFixturesValidateAgainstEnvelopeAndMappedData(t *testing.T) {
	server := New(Deps{})
	registry := server.API.OpenAPI().Components.Schemas
	envelope := registry.Map()["EventEnvelope"]
	if envelope == nil {
		t.Fatal("EventEnvelope component is missing")
	}

	for _, event := range eventpayload.StableEvents {
		fixtures := []string{event.Fixture}
		if event.MinimalFixture != "" {
			fixtures = append(fixtures, event.MinimalFixture)
		}
		for _, fixture := range fixtures {
			fixture := fixture
			t.Run(fixture, func(t *testing.T) {
				raw, err := os.ReadFile(filepath.Join("..", "eventpayload", "testdata", fixture))
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
				var decoded map[string]any
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatalf("decode fixture: %v", err)
				}
				validateSchema(t, registry, envelope, "event", decoded)
				if decoded["type"] != event.Type {
					t.Fatalf("fixture type = %#v, want %q", decoded["type"], event.Type)
				}
				payload := registry.Map()[event.SchemaName]
				if payload == nil {
					t.Fatalf("mapped payload component %s is missing", event.SchemaName)
				}
				validateSchema(t, registry, payload, "data", decoded["data"])
			})
		}
	}
}

func TestEventEnvelopeAcceptsUnknownFutureEventAndVersion(t *testing.T) {
	server := New(Deps{})
	registry := server.API.OpenAPI().Components.Schemas
	decoded := map[string]any{
		"type":            "email.future_event",
		"id":              "evt_future",
		"schema_version":  "2",
		"created_at":      "2030-01-02T03:04:05Z",
		"data":            map[string]any{"future_field": map[string]any{"nested": true}},
		"future_envelope": "preserved",
	}
	validateSchema(t, registry, registry.Map()["EventEnvelope"], "event", decoded)
}

func validateSchema(t *testing.T, registry huma.Registry, schema *huma.Schema, path string, value any) {
	t.Helper()
	result := &huma.ValidateResult{}
	huma.Validate(registry, schema, huma.NewPathBuffer([]byte(path), len(path)), huma.ModeReadFromServer, value, result)
	if len(result.Errors) > 0 {
		t.Fatalf("schema validation failed: %v", result.Errors)
	}
}

func stringsFromAny(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want array", value)
	}
	out := make([]string, len(raw))
	for i, value := range raw {
		var ok bool
		out[i], ok = value.(string)
		if !ok {
			t.Fatalf("array value %d = %#v, want string", i, value)
		}
	}
	return out
}
