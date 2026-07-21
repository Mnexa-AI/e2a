package httpapi

import "github.com/danielgtaylor/huma/v2"

// applyAuthenticationNullability expresses required-but-nullable referenced
// objects using JSON Schema 2020-12 anyOf. Huma deliberately rejects the
// nullable tag on object $refs, while this API needs authentication to remain
// present and null for outbound and providerless deliveries.
func (s *Server) applyAuthenticationNullability() {
	registry := s.API.OpenAPI().Components.Schemas
	for _, component := range []string{"MessageView", "Message", "EmailReceivedData"} {
		schema := registry.Map()[component]
		if schema == nil {
			panic("authentication schema owner is missing: " + component)
		}
		property := schema.Properties["authentication"]
		if property == nil || property.Ref == "" {
			panic("authentication property is not a schema reference: " + component)
		}
		minProperties, maxProperties := 1, 0
		schema.Properties["authentication"] = &huma.Schema{
			Description: property.Description,
			AnyOf: []*huma.Schema{
				{Ref: property.Ref},
				{
					// Huma/OpenAPI Generator cannot currently express a nullable
					// object $ref directly. This branch accepts null while its
					// contradictory object bounds reject every non-null object.
					Type:          huma.TypeObject,
					Nullable:      true,
					MinProperties: &minProperties,
					MaxProperties: &maxProperties,
				},
			},
		}
	}

	// identity.Message keeps database fields as strings, but its public export
	// JSON marshaler emits null when either identity is unavailable.
	message := registry.Map()["Message"]
	if message == nil {
		panic("message schema is missing")
	}
	for _, field := range []string{"header_from", "envelope_from"} {
		property := message.Properties[field]
		if property == nil {
			panic("message identity property is missing: " + field)
		}
		property.Nullable = true
	}
}
