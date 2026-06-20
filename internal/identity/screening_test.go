package identity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestScreeningEvents_CreateAndList covers the gate vs scan row shapes: a gate event
// carries subject_addr and leaves the scan-only columns null; a scan event carries
// detector/score/categories. Both round-trip through ListScreeningEventsByMessage.
func TestScreeningEvents_CreateAndList(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const msgID = "msg_screentest1"
	const agentID = "agent@screen.example.com"
	score := 0.87

	gate := identity.ScreeningEvent{
		ID:          identity.DeterministicScreeningEventID(msgID, identity.ScreeningSourceGate, identity.ReviewReasonSenderGate, ""),
		MessageID:   msgID,
		AgentID:     agentID,
		Direction:   "inbound",
		Source:      identity.ScreeningSourceGate,
		Reason:      identity.ReviewReasonSenderGate,
		Action:      "review",
		SubjectAddr: "attacker@evil.com",
	}
	scan := identity.ScreeningEvent{
		ID:         identity.DeterministicScreeningEventID(msgID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
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
	for _, ev := range []identity.ScreeningEvent{gate, scan} {
		if err := store.CreateScreeningEvent(ctx, ev); err != nil {
			t.Fatalf("CreateScreeningEvent(%s): %v", ev.Source, err)
		}
	}

	got, err := store.ListScreeningEventsByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("ListScreeningEventsByMessage: %v", err)
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

// TestScreeningEvents_Idempotent proves a deterministic id makes re-screening (e.g.
// an MTA-retried inbound delivery) a no-op rather than a duplicate.
func TestScreeningEvents_Idempotent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const msgID = "msg_idemp1"
	ev := identity.ScreeningEvent{
		ID:        identity.DeterministicScreeningEventID(msgID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
		MessageID: msgID,
		AgentID:   "a@x.com",
		Direction: "inbound",
		Source:    identity.ScreeningSourceScan,
		Reason:    identity.ReviewReasonInboundScan,
		Action:    "review",
		Detector:  "heuristics",
	}
	for i := 0; i < 3; i++ {
		if err := store.CreateScreeningEvent(ctx, ev); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := store.ListScreeningEventsByMessage(ctx, msgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("idempotent insert produced %d rows, want 1", len(got))
	}
}

// TestScreeningEvents_SoftRefAndAgentList proves message_id is a soft reference
// (events insert and list with no corresponding messages row — so the audit trail
// outlives the 30-day message TTL) and exercises ListScreeningEventsByAgent.
func TestScreeningEvents_SoftRefAndAgentList(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const agentID = "agent@audit.example.com"
	for i := 0; i < 3; i++ {
		ev := identity.ScreeningEvent{
			MessageID: "msg_ghost", // no such message row exists — soft ref
			AgentID:   agentID,
			Direction: "outbound",
			Source:    identity.ScreeningSourceScan,
			Reason:    identity.ReviewReasonOutboundScan,
			Action:    "flag",
			Detector:  "heuristics",
		}
		if err := store.CreateScreeningEvent(ctx, ev); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := store.ListScreeningEventsByAgent(ctx, agentID, 10)
	if err != nil {
		t.Fatalf("ListScreeningEventsByAgent: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("want 3 events for agent, got %d", len(got))
	}
}
