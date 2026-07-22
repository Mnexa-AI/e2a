package httpapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/messagelifecycle"
)

func lifecycleDocSection(t *testing.T, file, heading string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", file))
	if err != nil {
		t.Fatalf("read docs/%s: %v", file, err)
	}
	doc := string(b)
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("docs/%s missing %q", file, heading)
	}
	section := doc[start:]
	level := strings.SplitN(heading, " ", 2)[0]
	if next := strings.Index(section[len(heading):], "\n"+level+" "); next >= 0 {
		section = section[:len(heading)+next]
	}
	return section
}

func requireLifecycleDocText(t *testing.T, section string, required ...string) {
	t.Helper()
	for _, text := range required {
		if !strings.Contains(section, text) {
			t.Errorf("lifecycle documentation missing %q", text)
		}
	}
}

func TestMessageLifecycleDocsPublishClosedDiagnosticContract(t *testing.T) {
	api := lifecycleDocSection(t, "api.md", "### Message lifecycle diagnostic contract")
	requireLifecycleDocText(t, api,
		"GET /v1/agents/{email}/messages/{id}/lifecycle",
		"ascending `(occurred_at, id)`",
		"bound to both the owning agent and message ID",
		"`recipient` is nullable",
		"safe, bounded diagnostic metadata",
		"`correlation_ids`",
		"`reconstructed: true`",
		"does not fabricate",
		"`dedupe_key`",
		"additive",
		"delivered means the recipient mail server accepted the message; e2a does not observe or claim inbox placement.",
		"Screening and prompt-injection detections remain outside the lifecycle ledger.",
	)

	stages := map[messagelifecycle.Stage]bool{}
	reasons := make([]string, 0, len(messagelifecycle.Catalog()))
	for reason, definition := range messagelifecycle.Catalog() {
		stages[definition.Stage] = true
		reasons = append(reasons, string(reason))
	}
	for stage := range stages {
		if !strings.Contains(api, "`"+string(stage)+"`") {
			t.Errorf("docs/api.md lifecycle stage missing %q", stage)
		}
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		if !strings.Contains(api, "`"+reason+"`") {
			t.Errorf("docs/api.md lifecycle reason missing %q", reason)
		}
	}
}

func TestMessageLifecycleDocsMapEventsWithoutAbsorbingScreening(t *testing.T) {
	events := lifecycleDocSection(t, "events.md", "## Lifecycle transitions on events")
	requireLifecycleDocText(t, events,
		"`data.lifecycle_transitions`",
		"`email.received`", "`acceptance.inbound_smtp`", "`authentication.dmarc_pass`", "`queue.inbound_processing`",
		"`email.sent`", "`submission.upstream_accepted`", "`submission.local_loopback_accepted`",
		"`email.failed`", "`submission.provider_rejected`", "`submission.local_retries_exhausted`", "`submission.cancelled`",
		"`email.delivered`", "`delivery.recipient_server_accepted`",
		"`email.bounced`", "`delivery.permanent_bounce`", "`delivery.transient_bounce`", "`delivery.undetermined_bounce`",
		"`email.complained`", "`complaint.recipient_reported`",
		"`email.review_requested`", "`review.hold_created`",
		"`email.review_approved`", "`review.approved`", "`review.expired_approved`",
		"`email.review_rejected`", "`review.rejected`", "`review.expired_rejected`",
		"`domain.suppression_added`", "`suppression.hard_bounce_applied`", "`suppression.complaint_applied`",
		"`email.flagged` and `email.blocked` remain screening events outside the lifecycle ledger",
		"delivered means the recipient mail server accepted the message; e2a does not observe or claim inbox placement.",
	)
}
