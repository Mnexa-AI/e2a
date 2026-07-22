package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

type documentedLifecycleDefinition struct {
	stage     messagelifecycle.Stage
	outcome   messagelifecycle.Outcome
	retryable bool
}

func markdownTableRows(t *testing.T, section, header string) [][]string {
	t.Helper()
	start := strings.Index(section, header)
	if start < 0 {
		t.Fatalf("lifecycle documentation missing table header %q", header)
	}
	var rows [][]string
	for _, line := range strings.Split(section[start:], "\n")[2:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		parts := strings.Split(strings.Trim(line, "|"), "|")
		for i := range parts {
			parts[i] = strings.Trim(strings.TrimSpace(parts[i]), "`")
		}
		rows = append(rows, parts)
	}
	return rows
}

func TestMessageLifecycleDocsReasonTableExactlyMatchesCatalog(t *testing.T) {
	api := lifecycleDocSection(t, "api.md", "### Message lifecycle diagnostic contract")
	rows := markdownTableRows(t, api, "| Reason code | Stage | Outcome | Retryable |")
	got := map[messagelifecycle.ReasonCode]documentedLifecycleDefinition{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatalf("malformed lifecycle reason row: %v", row)
		}
		joined := strings.ToLower(strings.Join(row, " "))
		if strings.Contains(joined, "screening") || strings.Contains(joined, "prompt_injection") || strings.Contains(joined, "prompt-injection") {
			t.Errorf("screening row must not enter lifecycle reason table: %v", row)
		}
		reason := messagelifecycle.ReasonCode(row[0])
		if _, duplicate := got[reason]; duplicate {
			t.Errorf("duplicate lifecycle reason row %q", reason)
		}
		var retryable bool
		switch row[3] {
		case "true":
			retryable = true
		case "false":
		default:
			t.Errorf("reason %q retryable = %q, want true|false", reason, row[3])
		}
		got[reason] = documentedLifecycleDefinition{
			stage: messagelifecycle.Stage(row[1]), outcome: messagelifecycle.Outcome(row[2]), retryable: retryable,
		}
	}

	want := map[messagelifecycle.ReasonCode]documentedLifecycleDefinition{}
	for reason, definition := range messagelifecycle.Catalog() {
		want[reason] = documentedLifecycleDefinition{
			stage: definition.Stage, outcome: definition.Outcome, retryable: definition.Retryable,
		}
	}
	if len(got) != len(want) {
		t.Errorf("documented lifecycle reason count = %d, catalog = %d", len(got), len(want))
	}
	for reason, definition := range want {
		if gotDefinition, ok := got[reason]; !ok {
			t.Errorf("catalog reason missing from docs: %q", reason)
		} else if gotDefinition != definition {
			t.Errorf("reason %q docs = %+v, catalog = %+v", reason, gotDefinition, definition)
		}
	}
	for reason := range got {
		if _, ok := want[reason]; !ok {
			t.Errorf("docs contain non-catalog lifecycle reason %q", reason)
		}
	}

	outcomes := make([]string, 0)
	seenOutcomes := map[messagelifecycle.Outcome]bool{}
	for _, definition := range got {
		seenOutcomes[definition.outcome] = true
	}
	for outcome := range seenOutcomes {
		outcomes = append(outcomes, string(outcome))
	}
	slices.Sort(outcomes)
	wantOutcomes := []string{"accepted", "applied", "approved", "blocked", "bounced", "deferred", "delivered", "enqueued", "failed", "indeterminate", "passed", "pending", "rejected", "reported"}
	if !slices.Equal(outcomes, wantOutcomes) {
		t.Errorf("closed lifecycle outcomes = %v, want %v", outcomes, wantOutcomes)
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
		"coordinated OpenAPI, generated SDK, and handwritten client regeneration",
		"delivered means the recipient mail server accepted the message; e2a does not observe or claim inbox placement.",
		"Screening and prompt-injection detections remain outside the lifecycle ledger.",
	)

	stages := map[messagelifecycle.Stage]bool{}
	for reason, definition := range messagelifecycle.Catalog() {
		stages[definition.Stage] = true
		if !strings.Contains(api, "`"+string(reason)+"`") {
			t.Errorf("docs/api.md lifecycle reason missing %q", reason)
		}
	}
	gotStages := make([]string, 0, len(stages))
	for stage := range stages {
		gotStages = append(gotStages, string(stage))
	}
	slices.Sort(gotStages)
	wantStages := []string{"accepted", "authentication", "complaint", "delivery", "queued", "review", "submission", "suppression"}
	if !slices.Equal(gotStages, wantStages) {
		t.Errorf("closed lifecycle stages = %v, want %v", gotStages, wantStages)
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
		"DMARC `pass` → `authentication.dmarc_pass`",
		"DMARC `fail` → `authentication.dmarc_fail`",
		"DMARC `none` → `authentication.dmarc_none`",
		"DMARC `temperror` → `authentication.dmarc_temporary_error`",
		"DMARC `permerror` → `authentication.dmarc_permanent_error`",
	)
	authReasons := regexp.MustCompile("`(authentication\\.[^`]+)`").FindAllStringSubmatch(events, -1)
	gotAuth := map[string]bool{}
	for _, match := range authReasons {
		gotAuth[match[1]] = true
	}
	wantAuth := []string{
		"authentication.dmarc_pass", "authentication.dmarc_fail", "authentication.dmarc_none",
		"authentication.dmarc_temporary_error", "authentication.dmarc_permanent_error",
	}
	if len(gotAuth) != len(wantAuth) {
		t.Errorf("documented event authentication reasons = %v, want exactly %v", gotAuth, wantAuth)
	}
	for _, reason := range wantAuth {
		if !gotAuth[reason] {
			t.Errorf("event authentication mapping missing %q", reason)
		}
	}
}

func TestMessageLifecycleCompletedPlanUsesLiveDashboardRoute(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "superpowers", "plans", "2026-07-21-message-trust-ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	plan := string(b)
	if strings.Contains(plan, "`/api/v1/agents/.../lifecycle`") {
		t.Error("completed lifecycle plan still names the retired /api/v1 dashboard route")
	}
	if !strings.Contains(plan, "`/v1/agents/.../lifecycle`") {
		t.Error("completed lifecycle plan must name the live /v1 dashboard route")
	}
}
