package httpapi

import (
	"sort"
	"strings"
	"testing"
)

// These tests pin the account export's versioned-interior exemption
// (stability.go, exportOperationID/exportEnvelopeSchema):
//
//   - GET /v1/account/export is STABLE and its UserExport envelope (top-level
//     keys + schema_version) is frozen with the GA surface;
//   - every INTERIOR schema reachable from UserExport is exempt from the v1
//     freeze — versioned by schema_version instead — and must carry the
//     canonical `x-stability-level: beta` marker so oasdiff/compat tooling
//     excludes interior evolution from the stable gate;
//   - EXCEPT schemas the stable surface reaches on its own (via another
//     stable operation or a stable documentation component): those must stay
//     unmarked, because a global beta marker would silently degrade the
//     stable endpoints/event payloads that share them.
//
// The boundary below is COMPUTED from the rendered document (independently of
// the stamping code), not enumerated: a future field change inside an
// existing interior schema touches nothing here, and a brand-new interior
// schema is covered automatically — but if it ever renders UNMARKED (e.g. the
// stamping pass is weakened or removed) this test fails.

// exportSpecRefsIn collects component-schema names referenced anywhere under
// a node of the generic rendered document.
func exportSpecRefsIn(node any, out map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			const schemaPrefix = "#/components/schemas/"
			if strings.HasPrefix(ref, schemaPrefix) {
				out[strings.TrimPrefix(ref, schemaPrefix)] = true
			}
		}
		for _, v := range n {
			exportSpecRefsIn(v, out)
		}
	case []any:
		for _, v := range n {
			exportSpecRefsIn(v, out)
		}
	}
}

// exportSpecClosure expands root component names to everything transitively
// referenced through components.schemas of the rendered document.
func exportSpecClosure(t *testing.T, schemas map[string]any, roots map[string]bool) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	stack := make([]string, 0, len(roots))
	for name := range roots {
		stack = append(stack, name)
	}
	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		sc, ok := schemas[name].(map[string]any)
		if !ok {
			t.Fatalf("schema %q referenced but not defined", name)
		}
		next := map[string]bool{}
		exportSpecRefsIn(sc, next)
		for n := range next {
			if !seen[n] {
				stack = append(stack, n)
			}
		}
	}
	return seen
}

func TestExportEnvelopeStableInteriorVersioned(t *testing.T) {
	doc := renderSpec(t)
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)

	// --- The envelope itself is frozen stable GA surface. ---
	envelope, ok := schemas["UserExport"].(map[string]any)
	if !ok {
		t.Fatal("UserExport schema missing from the rendered spec — re-target the versioned-interior exemption and this test")
	}
	if got := envelope["x-stability-level"]; got != nil {
		t.Errorf("UserExport (the export envelope) must NOT carry x-stability-level — it is frozen stable; got %v", got)
	}
	props, _ := envelope["properties"].(map[string]any)
	sv, _ := props["schema_version"].(map[string]any)
	if sv == nil {
		t.Fatal("UserExport.schema_version missing — it is the version pivot of the whole exemption")
	}
	if got := sv["x-stability-level"]; got != nil {
		t.Errorf("UserExport.schema_version must stay stable (no x-stability-level), got %v", got)
	}
	svDoc, _ := sv["description"].(string)
	if !strings.Contains(svDoc, "branch on schema_version") {
		t.Errorf("UserExport.schema_version description must tell consumers to branch on schema_version; got %q", svDoc)
	}
	required, _ := envelope["required"].([]any)
	hasRequired := false
	for _, r := range required {
		if r == "schema_version" {
			hasRequired = true
		}
	}
	if !hasRequired {
		t.Error("UserExport.schema_version must remain a required field — consumers cannot branch on an absent version")
	}

	// --- The operation stays stable and documents the contract. ---
	op := operationByID(t, doc, "exportAccount")
	if got := op["x-stability-level"]; got != nil {
		t.Errorf("exportAccount is stable GA surface and must NOT carry x-stability-level, got %v", got)
	}
	opDesc, _ := op["description"].(string)
	for _, needle := range []string{"schema_version", "stable", "branch on schema_version"} {
		if !strings.Contains(opDesc, needle) {
			t.Errorf("exportAccount description must state the versioned-interior contract; missing %q", needle)
		}
	}

	// --- The exemption boundary, recomputed from the document. ---
	exportSet := exportSpecClosure(t, schemas, map[string]bool{"UserExport": true})

	// Stable surface that must keep the schemas it shares with the export
	// unmarked: (a) everything reachable from a stable (non-beta) operation
	// other than exportAccount; (b) everything reachable from a stable
	// operation-unreachable documentation component (event payloads etc.).
	allOpRoots := map[string]bool{}
	stableOtherRoots := map[string]bool{}
	paths, _ := doc["paths"].(map[string]any)
	for _, pi := range paths {
		item, _ := pi.(map[string]any)
		for _, rawOp := range item {
			opm, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			id, _ := opm["operationId"].(string)
			if id == "" {
				continue
			}
			roots := map[string]bool{}
			exportSpecRefsIn(opm["requestBody"], roots)
			exportSpecRefsIn(opm["responses"], roots)
			for name := range roots {
				allOpRoots[name] = true
				if id != "exportAccount" && opm["x-stability-level"] != "beta" {
					stableOtherRoots[name] = true
				}
			}
		}
	}
	opReachable := exportSpecClosure(t, schemas, allOpRoots)
	stableSurface := exportSpecClosure(t, schemas, stableOtherRoots)
	docRoots := map[string]bool{}
	for name, raw := range schemas {
		sc, _ := raw.(map[string]any)
		if opReachable[name] || exportSet[name] || sc["x-stability-level"] == "beta" {
			continue
		}
		docRoots[name] = true
	}
	for name := range exportSpecClosure(t, schemas, docRoots) {
		stableSurface[name] = true
	}

	var interiorBeta, interiorShared []string
	for name := range exportSet {
		if name == "UserExport" {
			continue
		}
		sc, _ := schemas[name].(map[string]any)
		if stableSurface[name] {
			// Shared with stable surface: must NOT be degraded to beta.
			if got := sc["x-stability-level"]; got != nil {
				t.Errorf("schema %s is shared with the stable surface and must NOT carry x-stability-level (marking it would degrade stable endpoints), got %v", name, got)
			}
			interiorShared = append(interiorShared, name)
			continue
		}
		if got := sc["x-stability-level"]; got != "beta" {
			t.Errorf("export interior schema %s must carry x-stability-level: beta (versioned by UserExport.schema_version, exempt from the v1 freeze), got %v", name, got)
		}
		interiorBeta = append(interiorBeta, name)
	}
	if len(interiorBeta) == 0 {
		t.Fatal("no export-only interior schemas found — test wiring is wrong")
	}

	// Anchors: pin today's boundary by name so an accidental re-plumbing of a
	// known schema (e.g. the export starts embedding MessageView, or a stable
	// endpoint starts returning identity.Message) is caught consciously.
	sort.Strings(interiorBeta)
	sort.Strings(interiorShared)
	mustBeBeta := []string{"APIKeyExportEntry", "AgentIdentity", "Domain", "Message", "OAuthConnectionEntry", "ProtectionEventExportEntry", "SuppressionExportEntry", "UsageEventEntry", "UserExportUser"}
	for _, name := range mustBeBeta {
		if !contains(interiorBeta, name) {
			t.Errorf("expected %s among the beta-marked export interior schemas; got %v", name, interiorBeta)
		}
	}
	// The canonical authentication components are reachable through both the
	// stable message/event surfaces and the export. They stay stable everywhere,
	// including inside the versioned export interior.
	for _, name := range []string{"AttachmentMetaView", "Authentication", "SPFResult", "DKIMResult", "DMARCResult"} {
		if !exportSet[name] {
			t.Errorf("expected %s inside the export closure — if the export stopped using it, revisit this test's shared-schema assumptions", name)
			continue
		}
		if !contains(interiorShared, name) {
			t.Errorf("expected %s to be recognized as shared stable surface (unmarked); got shared=%v beta=%v", name, interiorShared, interiorBeta)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
