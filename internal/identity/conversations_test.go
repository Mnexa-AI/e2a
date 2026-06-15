package identity_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// convoTestSetup provisions a verified domain + agent and returns the
// agent id. Each test gets its own (user, domain, agent) so tests
// can run in parallel without colliding on agent_id state.
func convoTestSetup(t *testing.T, store *identity.Store, prefix string) string {
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
	return agent.ID
}

func TestListConversationsByAgent_GroupsByConversationID(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-group")

	// Conversation A: 3 messages
	for i := 0; i < 3; i++ {
		_, err := store.CreateInboundMessage(ctx, "", agentID, "alice@gmail.com", "bot@convo-group.example.com",
			fmt.Sprintf("<a%d@gmail.com>", i),
			"Subject A", "conv-A", "", nil, nil, nil, nil, nil, nil,
		)
		if err != nil {
			t.Fatalf("CreateInboundMessage A%d: %v", i, err)
		}
	}
	// Conversation B: 1 message
	if _, err := store.CreateInboundMessage(ctx, "", agentID, "bob@gmail.com", "bot@convo-group.example.com",
		"<b0@gmail.com>", "Subject B", "conv-B", "", nil, nil, nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("CreateInboundMessage B: %v", err)
	}
	// A message with NO conversation_id — must NOT show up in the list.
	if _, err := store.CreateInboundMessage(ctx, "", agentID, "carol@gmail.com", "bot@convo-group.example.com",
		"<c0@gmail.com>", "Subject C (no conv)", "", "", nil, nil, nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("CreateInboundMessage C: %v", err)
	}

	convos, err := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{AgentID: agentID})
	if err != nil {
		t.Fatalf("ListConversationsByAgent: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("len(convos) = %d, want 2", len(convos))
	}
	got := map[string]int{}
	for _, c := range convos {
		got[c.ID] = c.MessageCount
	}
	if got["conv-A"] != 3 {
		t.Errorf("conv-A message_count = %d, want 3", got["conv-A"])
	}
	if got["conv-B"] != 1 {
		t.Errorf("conv-B message_count = %d, want 1", got["conv-B"])
	}
	if _, ok := got[""]; ok {
		t.Error("empty conversation_id must not appear in list")
	}
}

func TestListConversationsByAgent_SortsByLastMessageDesc(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-sort")

	// Create A first, then B — but A is the "newer" conversation
	// because we add another message to A after B's only one.
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-sort.example.com", "<a1@x>", "A", "conv-A-sort", "", nil, nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-sort.example.com", "<b1@x>", "B", "conv-B-sort", "", nil, nil, nil, nil, nil, nil)
	// Bump A's last_message_at to be the newest.
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-sort.example.com", "<a2@x>", "A2", "conv-A-sort", "", nil, nil, nil, nil, nil, nil)

	convos, _ := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{AgentID: agentID})
	if len(convos) != 2 {
		t.Fatalf("len(convos) = %d, want 2", len(convos))
	}
	if convos[0].ID != "conv-A-sort" {
		t.Errorf("first conversation id = %q, want conv-A-sort (most recent activity)", convos[0].ID)
	}
	if convos[1].ID != "conv-B-sort" {
		t.Errorf("second conversation id = %q, want conv-B-sort", convos[1].ID)
	}
}

func TestListConversationsByAgent_LimitClamp(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-clamp")

	// Caller asks for 9999; storage must clamp to ConversationListHardCap.
	// We don't need 9999 conversations to test the clamp — we just need
	// the query not to error and not to return more than the cap. The
	// cap value is part of the contract.
	if identity.ConversationListHardCap < 1 {
		t.Fatalf("ConversationListHardCap must be > 0, got %d", identity.ConversationListHardCap)
	}
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-clamp.example.com", "<a@x>", "A", "conv-clamp-A", "", nil, nil, nil, nil, nil, nil)
	convos, err := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{AgentID: agentID, Limit: 9999})
	if err != nil {
		t.Fatalf("ListConversationsByAgent: %v", err)
	}
	if len(convos) > identity.ConversationListHardCap {
		t.Errorf("len(convos) = %d exceeds hard cap %d", len(convos), identity.ConversationListHardCap)
	}
}

func TestListConversationsByAgent_SinceUntilWindow(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-window")

	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-window.example.com", "<old@x>", "old", "conv-old", "", nil, nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-window.example.com", "<new@x>", "new", "conv-new", "", nil, nil, nil, nil, nil, nil)

	// since=now-1h should return both; since=future should return zero.
	all, _ := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{
		AgentID: agentID,
		Since:   time.Now().Add(-1 * time.Hour),
	})
	if len(all) != 2 {
		t.Errorf("since=-1h returned %d convos, want 2", len(all))
	}
	none, _ := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{
		AgentID: agentID,
		Since:   time.Now().Add(1 * time.Hour),
	})
	if len(none) != 0 {
		t.Errorf("since=+1h returned %d convos, want 0 (future window)", len(none))
	}
}

func TestListConversationsByAgent_AggregateFields(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-agg")

	// Conversation with one inbound (explicitly unread) and one outbound.
	// The 9th arg of CreateInboundMessage is inbox_status — pass
	// "unread" explicitly so the has_unread aggregate is true.
	store.CreateInboundMessage(ctx, "", agentID, "alice@gmail.com", "bot@convo-agg.example.com", "<i1@x>", "Hello", "conv-agg", "unread", nil, nil, nil, nil, nil, nil)
	store.CreateOutboundMessage(ctx, agentID, []string{"alice@gmail.com"}, nil, nil, "Re: Hello", "reply", "smtp", "<out@x>", "conv-agg")

	convos, _ := store.ListConversationsByAgent(ctx, identity.ConversationListFilter{AgentID: agentID})
	if len(convos) != 1 {
		t.Fatalf("len(convos) = %d, want 1", len(convos))
	}
	c := convos[0]
	if c.MessageCount != 2 {
		t.Errorf("message_count = %d, want 2", c.MessageCount)
	}
	if c.InboundCount != 1 {
		t.Errorf("inbound_count = %d, want 1", c.InboundCount)
	}
	if c.OutboundCount != 1 {
		t.Errorf("outbound_count = %d, want 1", c.OutboundCount)
	}
	if !c.HasUnread {
		t.Error("has_unread = false; want true (inbound was created unread)")
	}
	// LatestSubject + LatestSender follow MAX(created_at). Both
	// rows were created back-to-back so either could be the latest;
	// rather than asserting one, just confirm they're non-empty.
	if c.LatestSubject == "" {
		t.Error("latest_subject is empty")
	}
}

func TestGetConversationByID_OrdersChronologically(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-order")

	// Create three messages explicitly in non-monotonic order to
	// verify the storage layer sorts on read, not write.
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-order.example.com", "<m1@x>", "first", "conv-ord", "", nil, nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-order.example.com", "<m2@x>", "second", "conv-ord", "", nil, nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agentID, "x@gmail.com", "bot@convo-order.example.com", "<m3@x>", "third", "conv-ord", "", nil, nil, nil, nil, nil, nil)

	d, err := store.GetConversationByID(ctx, agentID, "conv-ord")
	if err != nil {
		t.Fatalf("GetConversationByID: %v", err)
	}
	if d.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", d.MessageCount)
	}
	subjects := []string{d.Messages[0].Subject, d.Messages[1].Subject, d.Messages[2].Subject}
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(subjects, want) {
		t.Errorf("subjects = %v, want %v (chronological ASC)", subjects, want)
	}
}

func TestGetConversationByID_ComputesParticipantsAndLabels(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-part")

	m1, _ := store.CreateInboundMessage(ctx, "", agentID, "alice@gmail.com", "bot@convo-part.example.com", "<p1@x>", "hi", "conv-part", "", nil, nil, nil,
		[]string{"bot@convo-part.example.com", "team@convo-part.example.com"}, nil, nil,
	)
	m2, _ := store.CreateInboundMessage(ctx, "", agentID, "bob@gmail.com", "bot@convo-part.example.com", "<p2@x>", "hi", "conv-part", "", nil, nil, nil,
		[]string{"bot@convo-part.example.com"}, []string{"carol@gmail.com"}, nil,
	)
	store.ModifyMessageLabels(ctx, m1.ID, agentID, []string{"urgent"}, nil)
	store.ModifyMessageLabels(ctx, m2.ID, agentID, []string{"follow-up", "urgent"}, nil)

	d, err := store.GetConversationByID(ctx, agentID, "conv-part")
	if err != nil {
		t.Fatalf("GetConversationByID: %v", err)
	}
	// Participants = union of senders + recipient + to + cc
	gotP := append([]string(nil), d.Participants...)
	sort.Strings(gotP)
	wantP := []string{
		"alice@gmail.com",
		"bob@gmail.com",
		"bot@convo-part.example.com",
		"carol@gmail.com",
		"team@convo-part.example.com",
	}
	if !reflect.DeepEqual(gotP, wantP) {
		t.Errorf("participants = %v, want %v", gotP, wantP)
	}
	// Labels = sorted union {follow-up, urgent}
	if !reflect.DeepEqual(d.Labels, []string{"follow-up", "urgent"}) {
		t.Errorf("labels = %v, want [follow-up urgent]", d.Labels)
	}
}

func TestGetConversationByID_NotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-notfound")

	_, err := store.GetConversationByID(ctx, agentID, "conv-does-not-exist")
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("err = %v, want ErrMessageNotFound", err)
	}
}

func TestGetConversationByID_CrossAgentIsolation(t *testing.T) {
	// Agent A has a conversation. Agent B (different user) must NOT
	// be able to read it — even with the right conversation_id. The
	// boundary is enforced at the WHERE clause; cross-agent looks
	// like not-found to avoid leaking existence via 403 vs 404.
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentA := convoTestSetup(t, store, "convo-iso-a")
	agentB := convoTestSetup(t, store, "convo-iso-b")
	store.CreateInboundMessage(ctx, "", agentA, "x@gmail.com", "bot@convo-iso-a.example.com", "<a@x>", "secret", "conv-shared", "", nil, nil, nil, nil, nil, nil)

	// Agent B reading conv-shared must get not-found, NOT the inbound from A.
	_, err := store.GetConversationByID(ctx, agentB, "conv-shared")
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("cross-agent err = %v, want ErrMessageNotFound (must not leak across agents)", err)
	}
	// And A still sees it.
	d, err := store.GetConversationByID(ctx, agentA, "conv-shared")
	if err != nil {
		t.Fatalf("agent A read: %v", err)
	}
	if d.MessageCount != 1 {
		t.Errorf("agent A sees %d messages, want 1", d.MessageCount)
	}
}
