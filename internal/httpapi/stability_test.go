package httpapi

import (
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// These tests pin the forward-compatibility stance stamped by
// applyEvolutionStance (GA review #22/#23) so it can't silently regress:
//   - response schemas open (additionalProperties: true), request schemas
//     strict (additionalProperties: false), and never both for one schema;
//   - x-stability: experimental on every beta operation, and derived onto the
//     schemas only they use;
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
				out[ref[strings.LastIndex(ref, "/")+1:]] = true
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

// x-stability: experimental on exactly the beta surfaces; the stable core must
// NOT carry it.
func TestSpecExperimentalMarkers(t *testing.T) {
	doc := renderSpec(t)

	opExt := func(operationID string) any {
		paths, _ := doc["paths"].(map[string]any)
		for _, pi := range paths {
			item, _ := pi.(map[string]any)
			for _, op := range item {
				if opm, ok := op.(map[string]any); ok && opm["operationId"] == operationID {
					return opm["x-stability"]
				}
			}
		}
		t.Fatalf("operation %q not found", operationID)
		return nil
	}

	experimentalOps := []string{
		"listTemplates", "createTemplate", "getTemplate", "updateTemplate", "deleteTemplate", "validateTemplate",
		"listStarterTemplates", "getStarterTemplate",
		"getAgentProtection", "putAgentProtection",
	}
	for _, id := range experimentalOps {
		if got := opExt(id); got != "experimental" {
			t.Errorf("%s must carry x-stability: experimental (beta surface), got %v", id, got)
		}
	}
	for _, id := range []string{"sendMessage", "createAgent", "listMessages", "createWebhook", "listEvents"} {
		if got := opExt(id); got != nil {
			t.Errorf("%s is stable GA surface and must NOT carry x-stability, got %v", id, got)
		}
	}

	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	schemaExt := func(name string) any {
		sc, ok := schemas[name].(map[string]any)
		if !ok {
			t.Fatalf("schema %q not found", name)
		}
		return sc["x-stability"]
	}
	for _, name := range []string{"TemplateView", "CreateTemplateRequest", "StarterTemplateView", "ProtectionConfigView", "ProtectionConfigRequest"} {
		if got := schemaExt(name); got != "experimental" {
			t.Errorf("schema %s must carry x-stability: experimental, got %v", name, got)
		}
	}
	for _, name := range []string{"MessageView", "AgentView", "WebhookView", "SendEmailRequest", "ErrorEnvelope"} {
		if got := schemaExt(name); got != nil {
			t.Errorf("schema %s is stable and must NOT carry x-stability, got %v", name, got)
		}
	}

	// Field-level: the template hooks on the stable send op are beta.
	props := schemaProps(t, doc, "SendEmailRequest")
	for _, f := range []string{"template_alias", "template_id", "template_data"} {
		p, _ := props[f].(map[string]any)
		if p == nil || p["x-stability"] != "experimental" {
			t.Errorf("SendEmailRequest.%s must carry x-stability: experimental", f)
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

// The stability policy must ship inside the document itself (info.description
// is the contract's constitution).
func TestSpecStabilityPolicyPresent(t *testing.T) {
	doc := renderSpec(t)
	info, _ := doc["info"].(map[string]any)
	desc, _ := info["description"].(string)
	for _, needle := range []string{"Stability policy", "additive", "x-stability: experimental", "additionalProperties: true", "additionalProperties: false", "x-experimental-values"} {
		if !strings.Contains(desc, needle) {
			t.Errorf("info.description must state the stability policy; missing %q", needle)
		}
	}
}
