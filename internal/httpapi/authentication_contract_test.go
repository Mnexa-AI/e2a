package httpapi

import (
	"strings"
	"testing"
)

func TestAuthenticationContractFinalSchema(t *testing.T) {
	doc := renderSpec(t)

	for _, component := range []string{"MessageView", "Message", "EmailReceivedData"} {
		props := schemaProps(t, doc, component)
		if !typeIsNullable(props, "authentication") {
			t.Errorf("%s.authentication must be nullable", component)
		}
	}

	for _, component := range []string{"MessageView", "Message", "MessageSummaryView", "ReviewView", "EmailReceivedData"} {
		props := schemaProps(t, doc, component)
		if !typeIsNullable(props, "verified_domain") {
			t.Errorf("%s.verified_domain must be present and nullable", component)
		}
	}

	summary := schemaProps(t, doc, "MessageSummaryView")
	if _, exists := summary["authentication"]; exists {
		t.Error("MessageSummaryView must omit full authentication evidence")
	}
	review := schemaProps(t, doc, "ReviewView")
	if _, exists := review["from"]; exists {
		t.Error("ReviewView must not expose retired from")
	}
	for _, field := range []string{"header_from", "envelope_from", "verified_domain"} {
		if _, exists := review[field]; !exists {
			t.Errorf("ReviewView missing %s", field)
		}
	}

	spf := schemaProps(t, doc, "SPFResult")
	for _, status := range enumOf(spf, "status") {
		if status == "policy" {
			t.Error("SPFResult.status must not include unreachable policy")
		}
	}
	dmarc := schemaProps(t, doc, "DMARCResult")
	if got := enumOf(dmarc, "policy"); !setEqual(got, "none", "quarantine", "reject") {
		t.Errorf("DMARCResult.policy enum = %v", got)
	}
	alignedBy, _ := dmarc["aligned_by"].(map[string]any)
	items, _ := alignedBy["items"].(map[string]any)
	gotItems, _ := items["enum"].([]any)
	if len(gotItems) != 2 || gotItems[0] != "spf" || gotItems[1] != "dkim" {
		t.Errorf("DMARCResult.aligned_by item enum = %v", gotItems)
	}

	for _, component := range []string{"Authentication", "SPFResult", "DKIMResult", "DMARCResult"} {
		for field, raw := range schemaProps(t, doc, component) {
			property, _ := raw.(map[string]any)
			if property["description"] == nil {
				t.Errorf("%s.%s needs a description", component, field)
			}
		}
	}
}

func TestAuthenticationFieldsDocumentTheDeveloperTrustRule(t *testing.T) {
	doc := renderSpec(t)

	for _, component := range []string{"MessageView", "Message", "EmailReceivedData"} {
		property, _ := schemaProps(t, doc, component)["authentication"].(map[string]any)
		description, _ := property["description"].(string)
		for _, phrase := range []string{
			"Only dmarc.status=pass authenticates the RFC 5322 From domain",
			"does not authenticate the mailbox local part, a person, or message content",
			"Null means there was no authenticating inbound SMTP peer",
		} {
			if !strings.Contains(description, phrase) {
				t.Errorf("%s.authentication description %q missing %q", component, description, phrase)
			}
		}
	}
}
