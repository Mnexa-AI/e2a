package httpapi

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/danielgtaylor/huma/v2"
)

// EventEnvelope is the documentation schema for the push wire object
// shared by webhook deliveries and WebSocket frames. The runtime publisher
// uses webhookpub.Envelope; this map-typed data field is intentional so the
// published base schema stays open to unknown event types.
type EventEnvelope struct {
	Type          string         `json:"type" doc:"Open event type; clients must tolerate unknown values."`
	ID            string         `json:"id" doc:"Stable across retries and push channels; use it to deduplicate at-least-once delivery."`
	SchemaVersion string         `json:"schema_version" doc:"Open envelope-version string; the current server emits 1."`
	CreatedAt     time.Time      `json:"created_at" format:"date-time"`
	Data          map[string]any `json:"data" nullable:"false" doc:"Event-specific payload. Open at the envelope level; use x-e2a-event-data-schemas for stable event payloads."`
}

// registerEventPayloadSchemas publishes the canonical typed per-event `data`
// payloads (internal/eventpayload) as NAMED component schemas in the OpenAPI
// document — EmailReceivedData, EmailSentData, … — so SDK codegen and the
// docs renderer can reference the frozen per-event shapes.
//
// Mechanism: no operation's request/response embeds these types (the event
// envelope's `data` property deliberately stays OPEN — additionalProperties —
// so unknown/beta event types keep validating), so Huma would never emit them
// on its own. We pull each type through the API's shared schema registry
// (Components.Schemas.Schema, the same registry jsonResponse uses), which
// registers it under the hinted name and hoists it into components.schemas of
// the rendered spec. The event-type documentation (EventJSON.data and
// docs/events.md) references the schemas DESCRIPTIVELY — there is
// intentionally no oneOf/discriminator on the envelope.
//
// Beta events (email.flagged, email.blocked, email.review_requested,
// email.review_approved, email.review_rejected) have no schema here — their
// payloads are explicitly open/unstable.
func (s *Server) registerEventPayloadSchemas() {
	registry := s.API.OpenAPI().Components.Schemas
	names := make([]string, 0, len(eventpayload.StableEvents))
	for _, event := range eventpayload.StableEvents {
		schema := registry.Schema(reflect.TypeOf(event.Payload), true, event.SchemaName)
		// The registry MUST intern the type under the exact hinted name — the
		// docs reference these names, and a silent rename (e.g. a name
		// collision appending a suffix) would break every published pointer.
		if schema == nil || schema.Ref != "#/components/schemas/"+event.SchemaName {
			panic(fmt.Sprintf("event payload schema %s registered under an unexpected ref: %+v", event.SchemaName, schema))
		}
		names = append(names, event.SchemaName)
	}

	// Forward-compatibility stance: these are CONSUMER-direction (server →
	// client) payload schemas, so they MUST be open for additive evolution —
	// `additionalProperties: true`, like every response schema. Huma registers
	// structs strict (`additionalProperties: false`) by default, and because
	// no operation's request/response reaches these components, a generic
	// "open every response-reachable schema" pass would never touch them —
	// they'd ship strict and a spec-generated client would break on the first
	// additive payload field. So this registration opens them itself, walking
	// each component's nested object nodes and following $refs (AttachmentMeta
	// via EmailReceivedData.attachments) exactly like a response-schema stance
	// pass would.
	seen := map[string]bool{}
	for _, name := range names {
		openEventPayloadComponent(registry, name, seen)
	}

	envelope := registry.Schema(reflect.TypeOf(EventEnvelope{}), true, "EventEnvelope")
	if envelope == nil || envelope.Ref != "#/components/schemas/EventEnvelope" {
		panic(fmt.Sprintf("event envelope registered under an unexpected ref: %+v", envelope))
	}
	openEventPayloadComponent(registry, "EventEnvelope", seen)
	envelopeSchema := registry.Map()["EventEnvelope"]
	data := envelopeSchema.Properties["data"]
	if data == nil {
		panic("event envelope data property missing from registered schema")
	}
	// map[string]any is rendered as an unconstrained schema-valued map by
	// Huma. Publish the simpler explicit open-object posture consumers rely on.
	data.Type = huma.TypeObject
	data.AdditionalProperties = true
	if data.Extensions == nil {
		data.Extensions = map[string]any{}
	}
	mapping := make(map[string]any, len(eventpayload.StableEvents))
	for _, event := range eventpayload.StableEvents {
		mapping[event.Type] = "#/components/schemas/" + event.SchemaName
	}
	data.Extensions["x-e2a-event-data-schemas"] = mapping
}

// openEventPayloadComponent flips additionalProperties from the strict
// default (false) to true on the named component schema and every object node
// reachable from it — nested inline objects, array items, and $ref'd
// components (each opened once via seen). Map-typed nodes (whose
// additionalProperties is itself a schema, e.g. auth_headers) keep their
// value-schema and are recursed into.
func openEventPayloadComponent(registry huma.Registry, name string, seen map[string]bool) {
	if seen[name] {
		return
	}
	seen[name] = true
	sc := registry.Map()[name]
	if sc == nil {
		panic(fmt.Sprintf("event payload schema %s missing from the registry", name))
	}
	openEventPayloadNodes(sc, registry, seen)
}

func openEventPayloadNodes(sc *huma.Schema, registry huma.Registry, seen map[string]bool) {
	if sc == nil {
		return
	}
	if sc.Ref != "" {
		if i := strings.LastIndex(sc.Ref, "/"); i >= 0 {
			openEventPayloadComponent(registry, sc.Ref[i+1:], seen)
		}
		return
	}
	if v, ok := sc.AdditionalProperties.(bool); ok && !v {
		sc.AdditionalProperties = true
	}
	for _, p := range sc.Properties {
		openEventPayloadNodes(p, registry, seen)
	}
	openEventPayloadNodes(sc.Items, registry, seen)
	if ap, ok := sc.AdditionalProperties.(*huma.Schema); ok {
		openEventPayloadNodes(ap, registry, seen)
	}
	for _, sub := range sc.OneOf {
		openEventPayloadNodes(sub, registry, seen)
	}
	for _, sub := range sc.AnyOf {
		openEventPayloadNodes(sub, registry, seen)
	}
	for _, sub := range sc.AllOf {
		openEventPayloadNodes(sub, registry, seen)
	}
	openEventPayloadNodes(sc.Not, registry, seen)
}
