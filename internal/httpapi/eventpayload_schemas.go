package httpapi

import (
	"fmt"
	"reflect"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
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
	}
}
