package main

import "testing"

func TestNormalize30PreservesExistingGeneratorSemantics(t *testing.T) {
	document := map[string]any{
		"additionalProperties": false,
		"properties": map[string]any{
			"referenced": map[string]any{
				"$ref":        "#/components/schemas/Thing",
				"description": "kept in OpenAPI 3.0",
			},
			"nullable": map[string]any{
				"type": []any{"string", "null"},
			},
		},
	}

	normalize30(document)

	if document["additionalProperties"] != true {
		t.Fatalf("additionalProperties = %v, want true for generator parity", document["additionalProperties"])
	}
	properties := document["properties"].(map[string]any)
	referenced := properties["referenced"].(map[string]any)
	if _, ok := referenced["$ref"]; ok {
		t.Fatal("referenced schema still has a sibling $ref")
	}
	if referenced["description"] != "kept in OpenAPI 3.0" {
		t.Fatalf("description = %v", referenced["description"])
	}
	allOf := referenced["allOf"].([]any)
	if got := allOf[0].(map[string]any)["$ref"]; got != "#/components/schemas/Thing" {
		t.Fatalf("wrapped ref = %v", got)
	}
	nullable := properties["nullable"].(map[string]any)
	if nullable["type"] != "string" || nullable["nullable"] != true {
		t.Fatalf("nullable schema = %#v", nullable)
	}
}
