package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/outbound"
)

func specOperation(t *testing.T, doc map[string]any, operationID string) map[string]any {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	for _, rawPath := range paths {
		path, _ := rawPath.(map[string]any)
		for _, rawOperation := range path {
			operation, ok := rawOperation.(map[string]any)
			if ok && operation["operationId"] == operationID {
				return operation
			}
		}
	}
	t.Fatalf("operation %q not found in spec", operationID)
	return nil
}

func specResponseDescription(t *testing.T, operation map[string]any, status string) string {
	t.Helper()
	responses, _ := operation["responses"].(map[string]any)
	response, ok := responses[status].(map[string]any)
	if !ok {
		t.Fatalf("operation %q is missing response %s; responses=%v", operation["operationId"], status, keysOf(responses))
	}
	description, _ := response["description"].(string)
	return description
}

func requireContractText(t *testing.T, operationID, text string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("%s contract missing %q in %q", operationID, fragment, text)
		}
	}
}

// The composed-message ceiling is separate from the larger aggregate attachment
// allowance. It must be discoverable on every direct outbound operation, with
// the exact byte basis and the structured 413 details callers can branch on.
func TestSpecDocumentsComposedMessageCeiling(t *testing.T) {
	if outbound.MaxComposedMessageBytes != 10*1024*1024 {
		t.Fatalf("runtime composed-message ceiling = %d, want frozen 10 MiB", outbound.MaxComposedMessageBytes)
	}
	ceiling := fmt.Sprintf("10 MiB (%d bytes)", outbound.MaxComposedMessageBytes)
	doc := renderSpec(t)
	for _, operationID := range []string{"sendMessage", "replyToMessage", "forwardMessage"} {
		operation := specOperation(t, doc, operationID)
		description, _ := operation["description"].(string)
		requireContractText(t, operationID, description,
			ceiling,
			"subject + text + html + decoded attachment bytes",
		)

		responseDescription := specResponseDescription(t, operation, "413")
		requireContractText(t, operationID+" 413", responseDescription,
			"payload_too_large",
			"composed_bytes",
			"max_composed_bytes",
			fmt.Sprint(outbound.MaxComposedMessageBytes),
		)
	}

	errorProps := schemaProps(t, doc, "ErrorBody")
	details, _ := errorProps["details"].(map[string]any)
	detailsDescription, _ := details["description"].(string)
	requireContractText(t, "ErrorBody.details", detailsDescription,
		"send/reply/forward payload_too_large",
		"composed_bytes",
		"max_composed_bytes",
	)
	if strings.Contains(detailsDescription, "direct-send payload_too_large") {
		t.Errorf("ErrorBody.details still narrows composed-size details to send only: %q", detailsDescription)
	}
}

// Reviewer overrides are merged with the held draft before enforcing the same
// ceiling. The approval operation must explicitly declare that path and its 413;
// otherwise generated clients cannot discover a real runtime outcome.
func TestSpecDocumentsApproveOverrideComposedMessageCeiling(t *testing.T) {
	doc := renderSpec(t)
	operation := specOperation(t, doc, "approveReview")
	description, _ := operation["description"].(string)
	requireContractText(t, "approveReview", description,
		"merged outbound draft after applying reviewer overrides",
		fmt.Sprintf("10 MiB (%d bytes)", outbound.MaxComposedMessageBytes),
		"subject + text + html + decoded attachment bytes",
	)

	responseDescription := specResponseDescription(t, operation, "413")
	requireContractText(t, "approveReview 413", responseDescription,
		"payload_too_large",
		"merged outbound draft",
		fmt.Sprint(outbound.MaxComposedMessageBytes),
	)
}
