package httpapi

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/danielgtaylor/huma/v2"
)

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
	names := make([]string, 0, 9)
	for _, p := range []struct {
		typ  any
		name string
	}{
		{eventpayload.EmailReceivedData{}, "EmailReceivedData"},
		{eventpayload.EmailSentData{}, "EmailSentData"},
		{eventpayload.EmailFailedData{}, "EmailFailedData"},
		{eventpayload.EmailDeliveredData{}, "EmailDeliveredData"},
		{eventpayload.EmailBouncedData{}, "EmailBouncedData"},
		{eventpayload.EmailComplainedData{}, "EmailComplainedData"},
		{eventpayload.DomainSendingVerifiedData{}, "DomainSendingVerifiedData"},
		{eventpayload.DomainSendingFailedData{}, "DomainSendingFailedData"},
		{eventpayload.DomainSuppressionAddedData{}, "DomainSuppressionAddedData"},
	} {
		schema := registry.Schema(reflect.TypeOf(p.typ), true, p.name)
		// The registry MUST intern the type under the exact hinted name — the
		// docs reference these names, and a silent rename (e.g. a name
		// collision appending a suffix) would break every published pointer.
		if schema == nil || schema.Ref != "#/components/schemas/"+p.name {
			panic(fmt.Sprintf("event payload schema %s registered under an unexpected ref: %+v", p.name, schema))
		}
		names = append(names, p.name)
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
