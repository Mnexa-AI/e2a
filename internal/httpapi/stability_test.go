package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/webhookpub"
)

var betaOperationIDs = []string{
	"createTemplate",
	"deleteTemplate",
	"getAgentProtection",
	"getStarterTemplate",
	"getTemplate",
	"listStarterTemplates",
	"listTemplates",
	"putAgentProtection",
	"updateTemplate",
	"validateTemplate",
}

// These tests pin the forward-compatibility stance stamped by
// applyEvolutionStance (GA review #22/#23) so it can't silently regress:
//   - response schemas open (additionalProperties: true), request schemas
//     strict (additionalProperties: false), and never both for one schema;
//   - canonical x-stability-level: beta on every beta operation, derived onto
//     schemas only they use, with no duplicate lifecycle extension;
//   - x-experimental-values on the event-type fields whose value set has beta
//     members.

// specReachability recomputes request-body vs response-body schema
// reachability from the rendered document (independently of the stamping
// code, so the test doesn't just re-run the implementation).
func specReachability(t *testing.T, doc map[string]any) (request, response map[string]bool) {
	t.Helper()
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)

	var refsIn func(node any, out map[string]bool)
	refsIn = func(node any, out map[string]bool) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok {
				const schemaPrefix = "#/components/schemas/"
				if strings.HasPrefix(ref, schemaPrefix) {
					out[strings.TrimPrefix(ref, schemaPrefix)] = true
				}
			}
			for _, v := range n {
				refsIn(v, out)
			}
		case []any:
			for _, v := range n {
				refsIn(v, out)
			}
		}
	}
	closure := func(roots map[string]bool) map[string]bool {
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
			refsIn(sc, next)
			for n := range next {
				if !seen[n] {
					stack = append(stack, n)
				}
			}
		}
		return seen
	}

	reqRoots, respRoots := map[string]bool{}, map[string]bool{}
	paths, _ := doc["paths"].(map[string]any)
	for _, pi := range paths {
		item, _ := pi.(map[string]any)
		for _, op := range item {
			opm, ok := op.(map[string]any)
			if !ok {
				continue
			}
			refsIn(opm["requestBody"], reqRoots)
			refsIn(opm["responses"], respRoots)
		}
	}
	return closure(reqRoots), closure(respRoots)
}

// The stance itself: every response-reachable object schema tolerates unknown
// fields, every request-reachable one rejects them, and no schema is both.
func TestSpecEvolutionStance(t *testing.T) {
	doc := renderSpec(t)
	request, response := specReachability(t, doc)

	for name := range response {
		if request[name] {
			t.Errorf("schema %q is reachable from both a request and a response body — split the Go type (see stability.go)", name)
		}
	}
	if len(request) == 0 || len(response) == 0 {
		t.Fatal("reachability came back empty — test wiring is wrong")
	}

	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	var checkObjects func(name string, node any, wantOpen bool)
	checkObjects = func(name string, node any, wantOpen bool) {
		switch n := node.(type) {
		case map[string]any:
			if n["type"] == "object" {
				if _, isStruct := n["properties"]; isStruct {
					ap := n["additionalProperties"]
					if wantOpen && ap != true {
						t.Errorf("%s: response object schema must carry additionalProperties: true (got %v) — clients must tolerate additive fields", name, ap)
					}
					if !wantOpen && ap != false {
						t.Errorf("%s: request object schema must carry additionalProperties: false (got %v) — strict input validation is intentional", name, ap)
					}
				}
			}
			for _, v := range n {
				checkObjects(name, v, wantOpen)
			}
		case []any:
			for _, v := range n {
				checkObjects(name, v, wantOpen)
			}
		}
	}
	for name := range response {
		checkObjects(name, schemas[name], true)
	}
	for name := range request {
		checkObjects(name, schemas[name], false)
	}

	// Anchor both halves on the two operations whose behavior is most
	// load-bearing, so an accidental inversion is caught by name.
	if sc, _ := schemas["SendEmailRequest"].(map[string]any); sc["additionalProperties"] != false {
		t.Error("SendEmailRequest must stay strict (unknown field like `body` -> 422)")
	}
	if sc, _ := schemas["MessageView"].(map[string]any); sc["additionalProperties"] != true {
		t.Error("MessageView must be open (server may add response fields additively)")
	}
}

// The stance must also hold for operation-UNREACHABLE components. The typed
// per-event payload schemas (Email*Data / Domain*Data / AttachmentMeta,
// published by registerEventPayloadSchemas) are consumer-direction (server →
// client) but referenced by NO operation's request or response, so the
// response-reachability pass in applyEvolutionStance never sees them — they
// open themselves at registration. This test closes the gap the two-pass
// design leaves: every component schema that is not reachable from a request
// body (i.e. everything that is not strict-by-design input) must be open, so
// neither enforcement point can drift without failing here.
func TestSpecEvolutionStanceCoversUnreachableComponents(t *testing.T) {
	doc := renderSpec(t)
	request, response := specReachability(t, doc)

	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)

	var checkOpen func(name string, node any)
	checkOpen = func(name string, node any) {
		switch n := node.(type) {
		case map[string]any:
			if _, isRef := n["$ref"]; isRef {
				return
			}
			if n["type"] == "object" {
				if _, isStruct := n["properties"]; isStruct {
					if ap := n["additionalProperties"]; ap != true {
						t.Errorf("%s: non-request component object schema must carry additionalProperties: true (got %v) — consumers must tolerate additive fields", name, ap)
					}
				}
			}
			for _, v := range n {
				checkOpen(name, v)
			}
		case []any:
			for _, v := range n {
				checkOpen(name, v)
			}
		}
	}

	unreachable := map[string]bool{}
	for name := range schemas {
		if request[name] {
			continue // strict by design (input side of the stance)
		}
		checkOpen(name, schemas[name])
		if !response[name] {
			unreachable[name] = true
		}
	}

	// Anchor: the event payload components must exist, be operation-unreachable
	// (they are documentation/codegen components, not operation bodies), and
	// therefore be covered by the loop above — a rename or a future "attach
	// them to an operation" refactor must consciously revisit this test.
	//
	// Conscious exception: AttachmentMeta. Since the user-data export's Message
	// schema typed its `attachments` as []AttachmentMeta (one shape everywhere),
	// AttachmentMeta IS response-reachable (GET /v1/account/export → UserExport
	// → Message → AttachmentMeta) and is opened by the normal response pass in
	// applyEvolutionStance. It must still never become request-reachable.
	for _, name := range eventPayloadComponentNames {
		if _, ok := schemas[name]; !ok {
			t.Errorf("event payload component %s missing from the rendered spec", name)
			continue
		}
		if request[name] {
			t.Errorf("event payload component %s became request-reachable — it would now be forced strict, breaking additive payload evolution", name)
		}
		if name == "AttachmentMeta" {
			if !response[name] {
				t.Error("AttachmentMeta expected response-reachable via the export's Message.attachments — if that changed, revisit this exception")
			}
			continue
		}
		if !unreachable[name] {
			t.Errorf("event payload component %s expected to be operation-unreachable (got reachable) — update this test's assumptions consciously", name)
		}
	}
	if len(unreachable) == 0 {
		t.Fatal("no operation-unreachable components found — test wiring is wrong")
	}
}

// x-stability-level: beta on exactly the beta surfaces; no duplicate
// x-stability alias is emitted, and the stable core carries neither marker.
func TestSpecBetaMarkers(t *testing.T) {
	doc := renderSpec(t)

	opExt := func(operationID, extension string) any {
		paths, _ := doc["paths"].(map[string]any)
		for _, pi := range paths {
			item, _ := pi.(map[string]any)
			for _, op := range item {
				if opm, ok := op.(map[string]any); ok && opm["operationId"] == operationID {
					return opm[extension]
				}
			}
		}
		t.Fatalf("operation %q not found", operationID)
		return nil
	}

	for _, id := range betaOperationIDs {
		if got := opExt(id, "x-stability"); got != nil {
			t.Errorf("%s must not carry duplicate x-stability alias, got %v", id, got)
		}
		if got := opExt(id, "x-stability-level"); got != "beta" {
			t.Errorf("%s must carry canonical x-stability-level: beta, got %v", id, got)
		}
	}
	for _, id := range []string{"sendMessage", "createAgent", "listMessages", "createWebhook", "listEvents", "deleteMessage", "restoreMessage", "restoreAgent", "deleteAgent"} {
		if got := opExt(id, "x-stability"); got != nil {
			t.Errorf("%s is stable GA surface and must NOT carry x-stability, got %v", id, got)
		}
		if got := opExt(id, "x-stability-level"); got != nil {
			t.Errorf("%s is stable GA surface and must NOT carry x-stability-level, got %v", id, got)
		}
	}

	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	schemaExt := func(name, extension string) any {
		sc, ok := schemas[name].(map[string]any)
		if !ok {
			t.Fatalf("schema %q not found", name)
		}
		return sc[extension]
	}
	for _, name := range []string{"TemplateView", "CreateTemplateRequest", "StarterTemplateView", "ProtectionConfigView", "ProtectionConfigRequest"} {
		if got := schemaExt(name, "x-stability"); got != nil {
			t.Errorf("schema %s must not carry duplicate x-stability alias, got %v", name, got)
		}
		if got := schemaExt(name, "x-stability-level"); got != "beta" {
			t.Errorf("schema %s must carry canonical x-stability-level: beta, got %v", name, got)
		}
	}
	for _, name := range []string{"MessageView", "AgentView", "WebhookView", "SendEmailRequest", "ErrorEnvelope", "DeleteMessageResult"} {
		if got := schemaExt(name, "x-stability"); got != nil {
			t.Errorf("schema %s is stable and must NOT carry x-stability, got %v", name, got)
		}
		if got := schemaExt(name, "x-stability-level"); got != nil {
			t.Errorf("schema %s is stable and must NOT carry x-stability-level, got %v", name, got)
		}
	}

	// Field-level: the template hooks on the stable send op are beta.
	props := schemaProps(t, doc, "SendEmailRequest")
	for _, f := range []string{"template_alias", "template_id", "template_data"} {
		p, _ := props[f].(map[string]any)
		if p != nil && p["x-stability"] != nil {
			t.Errorf("SendEmailRequest.%s must not carry duplicate x-stability alias", f)
		}
		if p == nil || p["x-stability-level"] != "beta" {
			t.Errorf("SendEmailRequest.%s must carry canonical x-stability-level: beta", f)
		}
	}

	// Value-level: the screening + review-hold event types, everywhere event
	// types are enumerated, matching the canonical Go list.
	for _, name := range []string{"CreateWebhookRequest", "UpdateWebhookRequest", "WebhookView", "CreateWebhookResponse"} {
		p, _ := schemaProps(t, doc, name)["events"].(map[string]any)
		if p == nil {
			t.Errorf("%s.events missing", name)
			continue
		}
		raw, _ := p["x-experimental-values"].([]any)
		got := make([]string, 0, len(raw))
		for _, v := range raw {
			s, _ := v.(string)
			got = append(got, s)
		}
		if !setEqual(got, webhookpub.ExperimentalEventTypes...) {
			t.Errorf("%s.events x-experimental-values = %v, want %v", name, got, webhookpub.ExperimentalEventTypes)
		}
	}
}

func TestDocumentedBetaOperationsMatchOpenAPI(t *testing.T) {
	doc := renderSpec(t)
	var marked []string
	paths, _ := doc["paths"].(map[string]any)
	for _, pathItem := range paths {
		item, _ := pathItem.(map[string]any)
		for _, operation := range item {
			op, ok := operation.(map[string]any)
			if !ok || op["x-stability-level"] != "beta" {
				continue
			}
			if id, ok := op["operationId"].(string); ok {
				marked = append(marked, id)
			}
		}
	}
	sort.Strings(marked)
	want := append([]string(nil), betaOperationIDs...)
	sort.Strings(want)
	if !reflect.DeepEqual(marked, want) {
		t.Errorf("OpenAPI beta operations = %v, want exact reviewed inventory %v", marked, want)
	}

	apiDoc, err := os.ReadFile(filepath.Join("..", "..", "docs", "api.md"))
	if err != nil {
		t.Fatalf("read docs/api.md: %v", err)
	}
	text := string(apiDoc)
	start := strings.Index(text, "### Beta operations")
	if start < 0 {
		t.Fatal("docs/api.md is missing the exact Beta operations inventory")
	}
	section := text[start+len("### Beta operations"):]
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	re := regexp.MustCompile("`([A-Za-z][A-Za-z0-9]*)`")
	var documented []string
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		if match := re.FindStringSubmatch(cells[1]); len(match) == 2 && match[1] != "operationId" {
			documented = append(documented, match[1])
		}
	}
	sortedDocumented := append([]string(nil), documented...)
	sort.Strings(sortedDocumented)
	if !reflect.DeepEqual(documented, sortedDocumented) {
		t.Errorf("docs/api.md beta operation table must be sorted by operationId: got %v", documented)
	}
	sort.Strings(documented)
	if !reflect.DeepEqual(documented, marked) {
		t.Errorf("docs/api.md beta operations = %v, OpenAPI marks %v", documented, marked)
	}
}

// The stability policy must ship inside the document itself (info.description
// is the contract's constitution).
func TestSpecStabilityPolicyPresent(t *testing.T) {
	doc := renderSpec(t)
	info, _ := doc["info"].(map[string]any)
	desc, _ := info["description"].(string)
	for _, needle := range []string{"Stability policy", "additive", "x-stability-level: beta", "additionalProperties: true", "additionalProperties: false", "x-experimental-values"} {
		if !strings.Contains(desc, needle) {
			t.Errorf("info.description must state the stability policy; missing %q", needle)
		}
	}
}
