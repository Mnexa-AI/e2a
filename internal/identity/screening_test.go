package identity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

// seedProtAgent creates a user + verified domain + agent so protection_events
// inserts satisfy the agent_id FK (migration 046). Returns the agent id (= email).
func seedProtAgent(t *testing.T, store *identity.Store, ctx context.Context, email, domain string) string {
	t.Helper()
	user, err := store.CreateOrGetUser(ctx, "o@"+domain, "O", "g-"+domain)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, email, domain, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return ag.ID
}

// TestProtectionEvents_CreateAndList covers the gate vs scan row shapes: a gate event
// carries subject_addr and leaves the scan-only columns null; a scan event carries
// detector/score/categories. Both round-trip through ListProtectionEventsByMessage.
func TestProtectionEvents_CreateAndList(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const msgID = "msg_screentest1"
	agentID := seedProtAgent(t, store, ctx, "agent@screen.example.com", "screen.example.com")
	score := 0.87

	gate := identity.ProtectionEvent{
		ID:          identity.DeterministicProtectionEventID(msgID, identity.ScreeningSourceGate, identity.ReviewReasonSenderGate, ""),
		MessageID:   msgID,
		AgentID:     agentID,
		Direction:   "inbound",
		Source:      identity.ScreeningSourceGate,
		Reason:      identity.ReviewReasonSenderGate,
		Action:      "review",
		SubjectAddr: "attacker@evil.com",
	}
	scan := identity.ProtectionEvent{
		ID:         identity.DeterministicProtectionEventID(msgID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
		MessageID:  msgID,
		AgentID:    agentID,
		Direction:  "inbound",
		Source:     identity.ScreeningSourceScan,
		Reason:     identity.ReviewReasonInboundScan,
		Action:     "block",
		Detector:   "heuristics",
		Score:      &score,
		Categories: json.RawMessage(`[{"name":"prompt_injection_direct","score":0.87}]`),
	}
	for _, ev := range []identity.ProtectionEvent{gate, scan} {
		if err := store.CreateProtectionEvent(ctx, ev); err != nil {
			t.Fatalf("CreateProtectionEvent(%s): %v", ev.Source, err)
		}
	}

	got, err := store.ListProtectionEventsByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("ListProtectionEventsByMessage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}

	var sawScan, sawGate bool
	for _, ev := range got {
		switch ev.Source {
		case identity.ScreeningSourceScan:
			sawScan = true
			if ev.Score == nil || *ev.Score < 0.86 || *ev.Score > 0.88 {
				t.Errorf("scan score not round-tripped: %v", ev.Score)
			}
			if ev.Detector != "heuristics" {
				t.Errorf("detector = %q, want heuristics", ev.Detector)
			}
			if len(ev.Categories) == 0 {
				t.Errorf("categories not round-tripped")
			}
		case identity.ScreeningSourceGate:
			sawGate = true
			if ev.SubjectAddr != "attacker@evil.com" {
				t.Errorf("subject_addr = %q", ev.SubjectAddr)
			}
			if ev.Score != nil {
				t.Errorf("gate row should have nil score, got %v", *ev.Score)
			}
			if len(ev.Categories) != 0 {
				t.Errorf("gate row should have nil categories, got %s", ev.Categories)
			}
		}
	}
	if !sawScan || !sawGate {
		t.Errorf("missing a row: sawScan=%v sawGate=%v", sawScan, sawGate)
	}
}

// TestProtectionEvents_Idempotent proves a deterministic id makes re-screening (e.g.
// an MTA-retried inbound delivery) a no-op rather than a duplicate.
func TestProtectionEvents_Idempotent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const msgID = "msg_idemp1"
	agentID := seedProtAgent(t, store, ctx, "agent@idem.example.com", "idem.example.com")
	ev := identity.ProtectionEvent{
		ID:        identity.DeterministicProtectionEventID(msgID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
		MessageID: msgID,
		AgentID:   agentID,
		Direction: "inbound",
		Source:    identity.ScreeningSourceScan,
		Reason:    identity.ReviewReasonInboundScan,
		Action:    "review",
		Detector:  "heuristics",
	}
	for i := 0; i < 3; i++ {
		if err := store.CreateProtectionEvent(ctx, ev); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := store.ListProtectionEventsByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("idempotent insert produced %d rows, want 1", len(got))
	}
}

// TestProtectionEvents_SoftRefAndAgentList proves message_id is a soft reference
// (events insert and list with no corresponding messages row — so the audit trail
// outlives any individual message lifecycle) and exercises ListProtectionEventsByAgent.
func TestProtectionEvents_SoftRefAndAgentList(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	agentID := seedProtAgent(t, store, ctx, "agent@audit.example.com", "audit.example.com")
	for i := 0; i < 3; i++ {
		ev := identity.ProtectionEvent{
			MessageID: "msg_ghost", // no such message row exists — soft ref
			AgentID:   agentID,
			Direction: "outbound",
			Source:    identity.ScreeningSourceScan,
			Reason:    identity.ReviewReasonOutboundScan,
			Action:    "flag",
			Detector:  "heuristics",
		}
		if err := store.CreateProtectionEvent(ctx, ev); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := store.ListProtectionEventsByAgent(ctx, agentID, 10)
	if err != nil {
		t.Fatalf("ListProtectionEventsByAgent: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("want 3 events for agent, got %d", len(got))
	}
}

// TestProtectionEvents_CascadeOnAgentDelete proves the migration-046 FK: deleting
// the agent removes its protection_events (and therefore so does account deletion,
// since agent_identities cascades from users). This is the GDPR-erasure guarantee
// the soft-ref table previously lacked.
func TestProtectionEvents_CascadeOnAgentDelete(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "o@casc.example.com", "O", "g-casc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "casc.example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	ag, err := store.CreateAgent(ctx, "agent@casc.example.com", "casc.example.com", "", "", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProtectionEvent(ctx, identity.ProtectionEvent{
		MessageID: "msg_casc", AgentID: ag.ID, Direction: "inbound",
		Source: identity.ScreeningSourceScan, Reason: identity.ReviewReasonInboundScan,
		Action: "block", Detector: "heuristics", SubjectAddr: "attacker@evil.com",
	}); err != nil {
		t.Fatalf("CreateProtectionEvent: %v", err)
	}

	// Deleting the agent must cascade-delete its protection_events.
	if _, err := pool.Exec(ctx, `DELETE FROM agent_identities WHERE id=$1`, ag.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM protection_events WHERE agent_id=$1`, ag.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("protection_events survived agent deletion: %d rows (FK cascade not applied)", n)
	}
}
