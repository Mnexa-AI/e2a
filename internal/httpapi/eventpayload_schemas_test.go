package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/tokencanopy/e2a/internal/eventpayload"
)

// eventPayloadComponentNames are every component schema published by
// registerEventPayloadSchemas, plus the nested components reachable from them
// (AttachmentMetaView via EmailReceivedData.attachments).
var eventPayloadComponentNames = func() []string {
	names := make([]string, 0, len(eventpayload.StableEvents)+2)
	for _, event := range eventpayload.StableEvents {
		names = append(names, event.SchemaName)
	}
	return append(names, "AttachmentMetaView", "AgentSuppressionAddedData")
}()

// TestEventPayloadSchemasAreOpen enforces the forward-compatibility invariant
// on the RENDERED spec: every event-payload component schema (and every
// nested object node inside it) must carry `additionalProperties: true`.
// These are consumer-direction (server → client) payload schemas, and they
// are NOT reachable from any operation's response — so a stance pass that
// opens only response-REACHABLE schemas would leave them strict, and a
// spec-generated client would reject the first additive payload field. The
// invariant must therefore be enforced here, not inherited by accident.
func TestEventPayloadSchemasAreOpen(t *testing.T) {
	raw, err := json.Marshal(New(Deps{}).API.OpenAPI())
	if err != nil {
		t.Fatalf("render spec: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse rendered spec: %v", err)
	}

	for _, name := range eventPayloadComponentNames {
		rawSchema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("component schema %s missing from the rendered spec", name)
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(rawSchema, &schema); err != nil {
			t.Fatalf("parse component %s: %v", name, err)
		}
		assertObjectNodesOpen(t, name, schema)
	}
}

// assertObjectNodesOpen walks a rendered schema node and fails on any object
// node whose additionalProperties is not `true`. Map-typed nodes (whose
// additionalProperties is itself a schema, e.g. auth_headers' string map) are
// accepted and recursed into; $refs stop the walk (the referenced component is
// asserted on its own via eventPayloadComponentNames).
func assertObjectNodesOpen(t *testing.T, path string, node map[string]any) {
	t.Helper()
	if _, isRef := node["$ref"]; isRef {
		return
	}
	if node["type"] == "object" || node["properties"] != nil {
		switch ap := node["additionalProperties"].(type) {
		case bool:
			if !ap {
				t.Errorf("%s: object node has additionalProperties: false — event payload schemas must be open (additive evolution)", path)
			}
		case map[string]any:
			// Typed map (e.g. auth_headers: map[string]string) — the value
			// schema IS the openness; recurse into it.
			assertObjectNodesOpen(t, path+".additionalProperties", ap)
		default:
			t.Errorf("%s: object node has no additionalProperties — event payload schemas must carry an explicit additionalProperties: true", path)
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for pname, p := range props {
			if pm, ok := p.(map[string]any); ok {
				assertObjectNodesOpen(t, path+"."+pname, pm)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		assertObjectNodesOpen(t, path+".items", items)
	}
	for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
		if subs, ok := node[kw].([]any); ok {
			for _, sub := range subs {
				if sm, ok := sub.(map[string]any); ok {
					assertObjectNodesOpen(t, path+"."+kw, sm)
				}
			}
		}
	}
}
