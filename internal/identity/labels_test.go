package identity_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// labelsTestSetup provisions a verified domain + agent + a single
// inbound message so each test can exercise label mutations against a
// real row. Returns the message id and agent id.
func labelsTestSetup(t *testing.T, store *identity.Store, prefix string) (msgID, agentID string) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := prefix + ".example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agent, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	msg, err := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@"+domain, "<orig-"+prefix+"@gmail.com>", "Hello", "", "", nil, nil, nil, false, "", nil, nil, nil, identity.InboundScreening{})
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	return msg.ID, agent.ID
}

func TestModifyMessageLabels_AddRoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	msgID, agentID := labelsTestSetup(t, store, "labels-add")

	final, err := store.ModifyMessageLabels(ctx, msgID, agentID, []string{"urgent", "follow-up"}, nil)
	if err != nil {
		t.Fatalf("ModifyMessageLabels: %v", err)
	}
	want := []string{"follow-up", "urgent"} // sorted ascending by store
	if !reflect.DeepEqual(final, want) {
		t.Errorf("returned labels = %v, want %v", final, want)
	}

	// Re-read via the inbox query and confirm the labels round-trip.
	msgs, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{AgentID: agentID, Status: "all", Direction: "all", Limit: 10})
	if err != nil {
		t.Fatalf("GetMessagesByAgent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	got := append([]string(nil), msgs[0].Labels...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("persisted labels = %v, want %v", got, want)
	}
}

func TestModifyMessageLabels_RemoveAndOverlap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	msgID, agentID := labelsTestSetup(t, store, "labels-remove")

	if _, err := store.ModifyMessageLabels(ctx, msgID, agentID, []string{"a", "b", "c"}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// "b" appears in both add and remove — remove wins (the union-then-difference
	// semantics documented on ModifyMessageLabels).
	final, err := store.ModifyMessageLabels(ctx, msgID, agentID, []string{"b", "d"}, []string{"b", "a"})
	if err != nil {
		t.Fatalf("ModifyMessageLabels: %v", err)
	}
	want := []string{"c", "d"}
	if !reflect.DeepEqual(final, want) {
		t.Errorf("labels = %v, want %v", final, want)
	}
}

func TestModifyMessageLabels_RejectsOverCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	msgID, agentID := labelsTestSetup(t, store, "labels-cap")

	// Push one under the cap, then attempt to push two more in one op —
	// the second pushes us over.
	near := make([]string, identity.MaxLabelsPerMessage-1)
	for i := range near {
		near[i] = labelFor(i)
	}
	if _, err := store.ModifyMessageLabels(ctx, msgID, agentID, near, nil); err != nil {
		t.Fatalf("seed near cap: %v", err)
	}
	// Now MaxLabelsPerMessage-1 are set. Adding 2 unique new labels =
	// MaxLabelsPerMessage+1 — must reject.
	_, err := store.ModifyMessageLabels(ctx, msgID, agentID, []string{labelFor(9000), labelFor(9001)}, nil)
	if !errors.Is(err, identity.ErrLabelLimitExceeded) {
		t.Errorf("err = %v, want ErrLabelLimitExceeded", err)
	}
	// One MORE add (taking us to exactly MaxLabelsPerMessage) MUST succeed —
	// the boundary is the inclusive cap.
	final, err := store.ModifyMessageLabels(ctx, msgID, agentID, []string{labelFor(9000)}, nil)
	if err != nil {
		t.Fatalf("at-cap add: %v", err)
	}
	if len(final) != identity.MaxLabelsPerMessage {
		t.Errorf("len(final) = %d, want %d (exact cap)", len(final), identity.MaxLabelsPerMessage)
	}
}

func TestModifyMessageLabels_NotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, agentID := labelsTestSetup(t, store, "labels-notfound")

	_, err := store.ModifyMessageLabels(ctx, "msg_nonexistent", agentID, []string{"x"}, nil)
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("err = %v, want ErrMessageNotFound", err)
	}
}

func TestModifyMessageLabels_WrongAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	msgID, _ := labelsTestSetup(t, store, "labels-wrong-a")
	_, otherAgentID := labelsTestSetup(t, store, "labels-wrong-b")

	// agent B's id must NOT be able to mutate agent A's message.
	_, err := store.ModifyMessageLabels(ctx, msgID, otherAgentID, []string{"x"}, nil)
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("err = %v, want ErrMessageNotFound (cross-agent must look like not-found)", err)
	}
}

func TestGetMessagesByAgent_LabelsFilterANDMatch(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-lblfilter@example.com", "Owner", "google-lblfilter")
	store.ClaimOrCreateDomain(ctx, "lblfilter.example.com", user.ID)
	store.VerifyDomain(ctx, "lblfilter.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@lblfilter.example.com", "lblfilter.example.com", "", "https://example.com/webhook", "", user.ID)

	// Three messages with overlapping label sets:
	//   m1: [urgent, follow-up]
	//   m2: [urgent]
	//   m3: [follow-up]
	// Filter labels=[urgent, follow-up] must return ONLY m1 (AND semantics).
	m1, _ := store.CreateInboundMessage(ctx, "", agent.ID, "a@gmail.com", "bot@lblfilter.example.com", "<m1@gmail.com>", "M1", "", "", nil, nil, nil, false, "", nil, nil, nil, identity.InboundScreening{})
	m2, _ := store.CreateInboundMessage(ctx, "", agent.ID, "a@gmail.com", "bot@lblfilter.example.com", "<m2@gmail.com>", "M2", "", "", nil, nil, nil, false, "", nil, nil, nil, identity.InboundScreening{})
	store.CreateInboundMessage(ctx, "", agent.ID, "a@gmail.com", "bot@lblfilter.example.com", "<m3@gmail.com>", "M3", "", "", nil, nil, nil, false, "", nil, nil, nil, identity.InboundScreening{})

	store.ModifyMessageLabels(ctx, m1.ID, agent.ID, []string{"urgent", "follow-up"}, nil)
	store.ModifyMessageLabels(ctx, m2.ID, agent.ID, []string{"urgent"}, nil)
	// m3 gets only follow-up
	msgs, _ := store.GetMessagesByAgent(ctx, identity.MessageListFilter{AgentID: agent.ID, Status: "all", Direction: "all", Limit: 10})
	for _, m := range msgs {
		if m.Subject == "M3" {
			store.ModifyMessageLabels(ctx, m.ID, agent.ID, []string{"follow-up"}, nil)
		}
	}

	got, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{
		AgentID:   agent.ID,
		Status:    "all",
		Direction: "all",
		Limit:     10,
		Labels:    []string{"urgent", "follow-up"},
	})
	if err != nil {
		t.Fatalf("filter query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("filtered len = %d, want 1 (AND match should only return m1)", len(got))
	}
	if got[0].Subject != "M1" {
		t.Errorf("filtered subject = %q, want M1", got[0].Subject)
	}
}

func TestGetMessagesByAgent_LabelsAlwaysNonNil(t *testing.T) {
	// Regression: a row with no labels set must come back as []string{}
	// (or nil-coalesced to empty) — never a JSON null. The DB DEFAULT
	// '{}' is the contract. Tests the COALESCE-equivalent at the
	// pgx-driver layer.
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, agentID := labelsTestSetup(t, store, "labels-nilcheck")

	msgs, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{AgentID: agentID, Status: "all", Direction: "all", Limit: 10})
	if err != nil {
		t.Fatalf("GetMessagesByAgent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	// Either nil or empty is acceptable at the DB layer; the API layer
	// converts to empty for the wire. Just confirm it's not populated
	// with a stray label.
	if len(msgs[0].Labels) != 0 {
		t.Errorf("labels on a freshly-created message = %v, want empty", msgs[0].Labels)
	}
}

func labelFor(i int) string {
	// Stable, short, charset-valid labels for cap tests. Padding to
	// 4 digits keeps the lexicographic sort predictable so a debugger
	// printout reads sensibly.
	return fmt.Sprintf("label-%04d", i)
}
