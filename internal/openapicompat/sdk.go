package openapicompat

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CheckStableSDKSurface protects OpenAPI names that are part of generated SDK
// APIs but are not treated as wire-level breaking changes by oasdiff.
//
// The inputs must already have passed NormalizeStability and
// PruneExportInterior. That makes the canonical beta marker authoritative and
// preserves the account export's versioned-interior exemption.
func CheckStableSDKSurface(base, revision io.Reader) error {
	baseSurface, err := readSDKSurface(base)
	if err != nil {
		return fmt.Errorf("decode base SDK surface: %w", err)
	}
	revisionSurface, err := readSDKSurface(revision)
	if err != nil {
		return fmt.Errorf("decode revision SDK surface: %w", err)
	}

	var findings []string
	schemaNames := make([]string, 0, len(baseSurface.schemas))
	for name, beta := range baseSurface.schemas {
		if !beta {
			schemaNames = append(schemaNames, name)
		}
	}
	sort.Strings(schemaNames)
	for _, name := range schemaNames {
		revisionBeta, ok := revisionSurface.schemas[name]
		switch {
		case !ok:
			findings = append(findings, fmt.Sprintf(
				"[stable-sdk-schema-removed] stable component schema %q was removed or renamed; generated SDK model names are frozen",
				name,
			))
		case revisionBeta:
			findings = append(findings, fmt.Sprintf(
				"[stable-sdk-schema-stability-decreased] stable component schema %q was marked beta; generated SDK models cannot leave the stable surface",
				name,
			))
		}
	}

	operationIDs := make([]string, 0, len(baseSurface.operations))
	for id, operation := range baseSurface.operations {
		if !operation.beta {
			operationIDs = append(operationIDs, id)
		}
	}
	sort.Strings(operationIDs)
	for _, id := range operationIDs {
		baseOperation := baseSurface.operations[id]
		revisionOperation, ok := revisionSurface.operations[id]
		// Operation removal/rename and stability decreases already have dedicated
		// oasdiff findings. Only compare tags while the operation remains stable.
		if !ok || revisionOperation.beta {
			continue
		}
		if !slices.Equal(baseOperation.tags, revisionOperation.tags) {
			findings = append(findings, fmt.Sprintf(
				"[stable-sdk-operation-tags-changed] stable operation %q tags changed from %s to %s; generated SDK grouping is frozen",
				id, formatTags(baseOperation.tags), formatTags(revisionOperation.tags),
			))
		}
	}

	if len(findings) > 0 {
		return fmt.Errorf("%s", strings.Join(findings, "\n"))
	}
	return nil
}

type sdkSurface struct {
	// schemas maps every component schema name to whether it is beta.
	schemas map[string]bool
	// operations maps every operationId to its lifecycle and ordered tags.
	operations map[string]sdkOperation
}

type sdkOperation struct {
	beta bool
	tags []string
}

func readSDKSurface(r io.Reader) (sdkSurface, error) {
	var doc map[string]any
	if err := yaml.NewDecoder(r).Decode(&doc); err != nil {
		return sdkSurface{}, err
	}

	surface := sdkSurface{
		schemas:    map[string]bool{},
		operations: map[string]sdkOperation{},
	}
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for name, rawSchema := range schemas {
		surface.schemas[name] = nodeIsBeta(rawSchema)
	}

	paths, _ := doc["paths"].(map[string]any)
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOperation := range item {
			if !openAPIMethods[method] {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			id, _ := operation["operationId"].(string)
			if id == "" {
				continue
			}
			if _, duplicate := surface.operations[id]; duplicate {
				return sdkSurface{}, fmt.Errorf("duplicate operationId %q", id)
			}
			tags, err := readTags(operation["tags"])
			if err != nil {
				return sdkSurface{}, fmt.Errorf("%s %s operation %q: %w", strings.ToUpper(method), path, id, err)
			}
			surface.operations[id] = sdkOperation{beta: nodeIsBeta(operation), tags: tags}
		}
	}
	return surface, nil
}

var openAPIMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

func nodeIsBeta(raw any) bool {
	node, _ := raw.(map[string]any)
	return node[oasdiffExtension] == "beta" || node[e2aStabilityExtension] == "experimental"
}

func readTags(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tags is not an array")
	}
	tags := make([]string, len(items))
	for i, rawTag := range items {
		tag, ok := rawTag.(string)
		if !ok {
			return nil, fmt.Errorf("tags[%d] is not a string", i)
		}
		tags[i] = tag
	}
	return tags, nil
}

func formatTags(tags []string) string {
	if tags == nil {
		return "[]"
	}
	return fmt.Sprintf("%q", tags)
}
