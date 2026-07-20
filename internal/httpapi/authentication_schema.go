package httpapi

import "github.com/danielgtaylor/huma/v2"

// applyAuthenticationNullability expresses required-but-nullable referenced
// objects using JSON Schema 2020-12 anyOf. Huma deliberately rejects the
// nullable tag on object $refs, while this API needs authentication to remain
// present and null for outbound and providerless deliveries.
func (s *Server) applyAuthenticationNullability() {
	registry := s.API.OpenAPI().Components.Schemas
	for _, component := range []string{"MessageView", "MessageSummaryView", "EmailReceivedData"} {
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
}
