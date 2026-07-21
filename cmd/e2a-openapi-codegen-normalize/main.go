// e2a-openapi-codegen-normalize adapts valid OpenAPI 3.1 nullable object
// references to the OpenAPI Generator 7.16 dialect. It is an internal build
// tool; the canonical published document remains api/openapi.yaml.
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <input.yaml> <output.yaml>\n", os.Args[0])
		os.Exit(2)
	}

	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal("read input", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		fatal("parse input", err)
	}
	if err := normalizeDocument(document); err != nil {
		fatal("normalize authentication", err)
	}

	out, err := yaml.Marshal(document)
	if err != nil {
		fatal("encode output", err)
	}
	if err := os.WriteFile(os.Args[2], out, 0o600); err != nil {
		fatal("write output", err)
	}
}

func normalizeDocument(document map[string]any) error {
	// OpenAPI Generator 7.16 only honors nullable on referenced models when
	// operating in its OpenAPI 3.0 compatibility mode.
	document["openapi"] = "3.0.3"
	normalize30(document)

	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for _, name := range []string{"MessageView", "Message", "EmailReceivedData"} {
		schema, _ := schemas[name].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		property, _ := properties["authentication"].(map[string]any)
		alternatives, _ := property["anyOf"].([]any)
		if len(alternatives) != 2 {
			return fmt.Errorf("%s.authentication does not have the expected two-branch anyOf", name)
		}
		ref, _ := alternatives[0].(map[string]any)
		if ref["$ref"] != "#/components/schemas/Authentication" {
			return fmt.Errorf("%s.authentication has unexpected reference %v", name, ref["$ref"])
		}
		delete(property, "anyOf")
		property["allOf"] = []any{ref}
		property["nullable"] = true
	}
	return nil
}

func normalize30(value any) {
	switch node := value.(type) {
	case map[string]any:
		// Generator 7.16's OpenAPI 3.1 path historically ignored Huma's
		// additionalProperties:false. Preserve the generated clients' existing
		// forward-compatible unknown-field handling while switching only the
		// nullable-reference subset to its 3.0 parser.
		if additional, ok := node["additionalProperties"].(bool); ok && !additional {
			node["additionalProperties"] = true
		}
		// OpenAPI 3.0 ignores siblings next to $ref. Wrap the reference so
		// descriptions and stability metadata on referenced properties survive.
		if ref, ok := node["$ref"].(string); ok && len(node) > 1 {
			delete(node, "$ref")
			node["allOf"] = []any{map[string]any{"$ref": ref}}
		}
		if types, ok := node["type"].([]any); ok && len(types) == 2 {
			var concrete string
			hasNull := false
			for _, rawType := range types {
				if rawType == "null" {
					hasNull = true
				} else if typed, ok := rawType.(string); ok {
					concrete = typed
				}
			}
			if hasNull && concrete != "" {
				node["type"] = concrete
				node["nullable"] = true
			}
		}
		if node["contentEncoding"] == "base64" {
			delete(node, "contentEncoding")
			node["format"] = "byte"
		}
		if examples, ok := node["examples"].([]any); ok && len(examples) > 0 {
			delete(node, "examples")
			node["example"] = examples[0]
		}
		for _, child := range node {
			normalize30(child)
		}
	case []any:
		for _, child := range node {
			normalize30(child)
		}
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
