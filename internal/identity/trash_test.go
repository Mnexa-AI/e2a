package identity_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// trashTestSetup creates a user + verified domain + agent for trash tests.
func trashTestSetup(t *testing.T, store *identity.Store, slug string) (userID, agentID string) {
	t.Helper()
	ctx := context.Background()
	domain := slug + ".example.com"
	user, err := store.CreateOrGetUser(ctx, slug+"-owner@example.com", "Owner", "google-"+slug)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	a, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return user.ID, a.ID
}

func trashInbound(t *testing.T, store *identity.Store, agentID, recipient, subject string) *identity.Message {
	t.Helper()
	m, err := store.CreateInboundMessage(context.Background(), "", agentID,
		"alice@gmail.com", recipient, fmt.Sprintf("<%s@gmail.com>", identity.NewMessageID()),
		subject, "", "", []byte("raw"), nil, nil, false, "", nil, nil, nil,
		identity.InboundScreening{})
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	return m
}

// listIDs runs GetMessagesByAgent with the given trash flag and returns ids.
func listIDs(t *testing.T, store *identity.Store, agentID string, deleted bool) map[string]*identity.Message {
	t.Helper()
	msgs, err := store.GetMessagesByAgent(context.Background(), identity.MessageListFilter{
		AgentID: agentID, Direction: "all", Status: "all",
		Descending: true, Limit: 100, Deleted: deleted,
	})
	if err != nil {
		t.Fatalf("GetMessagesByAgent(deleted=%v): %v", deleted, err)
	}
	out := map[string]*identity.Message{}
	for i := range msgs {
		out[msgs[i].ID] = &msgs[i]
	}
	return out
}

func TestMessageTrashLifecycle(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := trashTestSetup(t, store, "msg-trash")

	kept := trashInbound(t, store, agentID, "bot@msg-trash.example.com", "kept")
	doomed := trashInbound(t, store, agentID, "bot@msg-trash.example.com", "doomed")

	// Trash `doomed`.
	if err := store.SoftDeleteMessage(ctx, doomed.ID, agentID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	// Idempotent on an already-trashed message.
	if err := store.SoftDeleteMessage(ctx, doomed.ID, agentID); err != nil {
		t.Fatalf("SoftDeleteMessage (repeat): %v", err)
	}

	// Live list excludes it; trash list carries it with DeletedAt set.
	live := listIDs(t, store, agentID, false)
	if _, ok := live[doomed.ID]; ok {
		t.Error("trashed message still in live list")
	}
	if _, ok := live[kept.ID]; !ok {
		t.Error("live message missing from live list")
	}
	trash := listIDs(t, store, agentID, true)
	tm, ok := trash[doomed.ID]
	if !ok {
		t.Fatal("trashed message missing from trash list")
	}
	if tm.DeletedAt == nil {
		t.Error("trash list row has nil DeletedAt")
	}
	if _, ok := trash[kept.ID]; ok {
		t.Error("live message leaked into trash list")
	}

	// Single-message get still opens it (trash detail view), annotated.
	got, err := store.GetMessageWithContent(ctx, doomed.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent(trashed): %v", err)
	}
	if got.DeletedAt == nil {
		t.Error("GetMessageWithContent(trashed): DeletedAt is nil")
	}

	// Reply/threading anchors treat it as gone.
	if _, err := store.GetInboundMessage(ctx, doomed.ID); err == nil {
		t.Error("GetInboundMessage returned a trashed message")
	}
	if _, err := store.GetRepliableMessage(ctx, doomed.ID); err == nil {
		t.Error("GetRepliableMessage returned a trashed message")
	}

	// Restore: back in the live list, gone from trash, DeletedAt cleared.
	if err := store.RestoreMessage(ctx, doomed.ID, agentID); err != nil {
		t.Fatalf("RestoreMessage: %v", err)
	}
	if _, ok := listIDs(t, store, agentID, false)[doomed.ID]; !ok {
		t.Error("restored message missing from live list")
	}
	if _, ok := listIDs(t, store, agentID, true)[doomed.ID]; ok {
		t.Error("restored message still in trash list")
	}

	// Restore/purge on a live message → ErrNotInTrash.
	if err := store.RestoreMessage(ctx, doomed.ID, agentID); !errors.Is(err, identity.ErrNotInTrash) {
		t.Errorf("RestoreMessage(live) = %v, want ErrNotInTrash", err)
	}
	if err := store.PurgeMessage(ctx, doomed.ID, agentID); !errors.Is(err, identity.ErrNotInTrash) {
		t.Errorf("PurgeMessage(live) = %v, want ErrNotInTrash", err)
	}
	// Missing message → ErrMessageNotFound.
	if err := store.RestoreMessage(ctx, "msg_nope", agentID); !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("RestoreMessage(missing) = %v, want ErrMessageNotFound", err)
	}
	if err := store.SoftDeleteMessage(ctx, "msg_nope", agentID); !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("SoftDeleteMessage(missing) = %v, want ErrMessageNotFound", err)
	}

	// Delete forever: trash then purge; the row is gone for good.
	if err := store.SoftDeleteMessage(ctx, doomed.ID, agentID); err != nil {
		t.Fatalf("SoftDeleteMessage (again): %v", err)
	}
	if err := store.PurgeMessage(ctx, doomed.ID, agentID); err != nil {
		t.Fatalf("PurgeMessage: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id = $1`, doomed.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("purged message still present (n=%d, err=%v)", n, err)
	}
}

// TestSoftDeleteMessageHeldGuard: a pending_review message (either direction)
// cannot be trashed — the review queue is its resolution surface.
func TestSoftDeleteMessageHeldGuard(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := trashTestSetup(t, store, "held-trash")

	held, err := store.CreatePendingOutboundMessage(ctx, agentID,
		[]string{"x@example.com"}, nil, nil, "held", "body", "", nil,
		"send", "", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	if err := store.SoftDeleteMessage(ctx, held.ID, agentID); !errors.Is(err, identity.ErrMessageHeld) {
		t.Errorf("SoftDeleteMessage(held) = %v, want ErrMessageHeld", err)
	}
	_ = pool // pool used via store
}

// TestRestoreMessageShiftsExpiry: the natural-expiry clock is suspended while
// a message sits in the trash — restore pushes expires_at forward by the time
// spent trashed, so the message resumes with the lifetime it had left.
func TestRestoreMessageShiftsExpiry(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := trashTestSetup(t, store, "shift-trash")

	m := trashInbound(t, store, agentID, "bot@shift-trash.example.com", "shift")
	if err := store.SoftDeleteMessage(ctx, m.ID, agentID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	// Simulate 20 days in the trash: the message would be past its natural
	// 10-day TTL if the clock kept ticking.
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET deleted_at = deleted_at - interval '20 days',
		                     expires_at = expires_at - interval '20 days',
		                     created_at = created_at - interval '20 days'
		  WHERE id = $1`, m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := store.RestoreMessage(ctx, m.ID, agentID); err != nil {
		t.Fatalf("RestoreMessage: %v", err)
	}
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM messages WHERE id = $1`, m.ID).Scan(&expires); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	// It had ~10 days of life left when trashed, so post-restore expiry must
	// be ~10 days out (not in the past).
	left := time.Until(expires)
	if left < 9*24*time.Hour || left > 11*24*time.Hour {
		t.Errorf("post-restore lifetime = %v, want ~10 days", left)
	}
	// And it is visible again.
	if _, ok := listIDs(t, store, agentID, false)[m.ID]; !ok {
		t.Error("restored message missing from live list")
	}
}

// TestDeleteExpiredMessagesTrashArms: the janitor deletes live rows past
// natural expiry and trashed rows past TrashRetention — but never a trashed
// row still inside its retention window, even when its (suspended) natural
// expiry has passed.
func TestDeleteExpiredMessagesTrashArms(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := trashTestSetup(t, store, "janitor-trash")

	expired := trashInbound(t, store, agentID, "bot@janitor-trash.example.com", "expired-live")
	freshTrash := trashInbound(t, store, agentID, "bot@janitor-trash.example.com", "fresh-trash")
	staleTrash := trashInbound(t, store, agentID, "bot@janitor-trash.example.com", "stale-trash")
	keeper := trashInbound(t, store, agentID, "bot@janitor-trash.example.com", "keeper")

	// Live row past natural expiry → deleted.
	if _, err := pool.Exec(ctx, `UPDATE messages SET expires_at = now() - interval '1 hour' WHERE id = $1`, expired.ID); err != nil {
		t.Fatalf("backdate expired: %v", err)
	}
	// Trashed yesterday, natural expiry long past → kept (clock suspended).
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET deleted_at = now() - interval '1 day', expires_at = now() - interval '5 days' WHERE id = $1`,
		freshTrash.ID); err != nil {
		t.Fatalf("backdate freshTrash: %v", err)
	}
	// Trashed 31 days ago → past TrashRetention, purged.
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET deleted_at = now() - interval '31 days' WHERE id = $1`, staleTrash.ID); err != nil {
		t.Fatalf("backdate staleTrash: %v", err)
	}

	if _, err := store.DeleteExpiredMessages(ctx); err != nil {
		t.Fatalf("DeleteExpiredMessages: %v", err)
	}

	var got []string
	rows, err := pool.Query(ctx, `SELECT id FROM messages WHERE agent_id = $1`, agentID)
	if err != nil {
		t.Fatalf("query survivors: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	want := map[string]bool{freshTrash.ID: true, keeper.ID: true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Errorf("survivors = %v, want exactly {freshTrash=%s, keeper=%s}", got, freshTrash.ID, keeper.ID)
	}
}

func TestAgentTrashLifecycle(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := trashTestSetup(t, store, "agent-trash")
	msg := trashInbound(t, store, agentID, "bot@agent-trash.example.com", "inbox mail")

	if err := store.SoftDeleteAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}
	// Repeat soft delete: already trashed → error (not found among live).
	if err := store.SoftDeleteAgent(ctx, agentID, userID); err == nil {
		t.Error("SoftDeleteAgent(already trashed): expected error")
	}

	// Live lookups treat it as nonexistent (inbound relay + API resolution).
	if _, err := store.GetAgentByID(ctx, agentID); err == nil {
		t.Error("GetAgentByID returned a trashed agent")
	}
	if _, err := store.GetAgentByEmail(ctx, agentID); err == nil {
		t.Error("GetAgentByEmail returned a trashed agent")
	}
	// Any-state lookup finds it, annotated.
	anyState, err := store.GetAgentByIDAnyState(ctx, agentID)
	if err != nil {
		t.Fatalf("GetAgentByIDAnyState: %v", err)
	}
	if anyState.DeletedAt == nil {
		t.Error("GetAgentByIDAnyState: DeletedAt is nil for a trashed agent")
	}

	// Live list excludes; trash list includes.
	liveAgents, err := store.ListAgentsByUser(ctx, userID, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListAgentsByUser: %v", err)
	}
	for _, a := range liveAgents {
		if a.ID == agentID {
			t.Error("trashed agent still in live list")
		}
	}
	trashed, err := store.ListDeletedAgentsByUser(ctx, userID, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListDeletedAgentsByUser: %v", err)
	}
	if len(trashed) != 1 || trashed[0].ID != agentID {
		t.Fatalf("trash list = %+v, want exactly the trashed agent", trashed)
	}
	if trashed[0].DeletedAt == nil {
		t.Error("trash list row has nil DeletedAt")
	}

	// Restore: back everywhere, messages intact.
	if err := store.RestoreAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("RestoreAgent: %v", err)
	}
	if _, err := store.GetAgentByID(ctx, agentID); err != nil {
		t.Errorf("GetAgentByID after restore: %v", err)
	}
	if _, ok := listIDs(t, store, agentID, false)[msg.ID]; !ok {
		t.Error("agent's message missing after restore")
	}
	// Restore on a live agent → ErrNotInTrash.
	if err := store.RestoreAgent(ctx, agentID, userID); !errors.Is(err, identity.ErrNotInTrash) {
		t.Errorf("RestoreAgent(live) = %v, want ErrNotInTrash", err)
	}

	// Wrong owner can neither trash nor restore.
	otherUser, err := store.CreateOrGetUser(ctx, "intruder@example.com", "X", "google-intruder-trash")
	if err != nil {
		t.Fatalf("CreateOrGetUser(intruder): %v", err)
	}
	if err := store.SoftDeleteAgent(ctx, agentID, otherUser.ID); err == nil {
		t.Error("SoftDeleteAgent by non-owner succeeded")
	}
}

// TestAgentTrashPausesMessageClocks: while an inbox sits in the trash its
// messages' natural expiry is suspended (the janitor must not eat them), and
// RestoreAgent gives the time back — expires_at and a held draft's
// approval_expires_at shift forward by the time spent trashed, so restore
// returns the inbox exactly as it was.
func TestAgentTrashPausesMessageClocks(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := trashTestSetup(t, store, "pause-trash")

	msg := trashInbound(t, store, agentID, "bot@pause-trash.example.com", "keep me")
	held, err := store.CreatePendingOutboundMessage(ctx, agentID,
		[]string{"x@example.com"}, nil, nil, "held", "body", "", nil,
		"send", "", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	if err := store.SoftDeleteAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}
	// Simulate 20 days in the trash: both messages would be far past their
	// natural clocks if those kept ticking.
	if _, err := pool.Exec(ctx,
		`UPDATE agent_identities SET deleted_at = deleted_at - interval '20 days' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("backdate agent: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET expires_at = expires_at - interval '20 days',
		                     created_at = created_at - interval '20 days',
		                     approval_expires_at = approval_expires_at - interval '20 days'
		  WHERE agent_id = $1`, agentID); err != nil {
		t.Fatalf("backdate messages: %v", err)
	}

	// The janitor must not touch the trashed inbox's messages even though
	// their expires_at is long past.
	if _, err := store.DeleteExpiredMessages(ctx); err != nil {
		t.Fatalf("DeleteExpiredMessages: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id = $1`, agentID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("messages of trashed agent survived = %d (err=%v), want 2", n, err)
	}

	if err := store.RestoreAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("RestoreAgent: %v", err)
	}
	// The inbound message resumes with ~10 days of life left (it had all of
	// MessageTTL left when the agent was trashed).
	var expires, approval time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM messages WHERE id = $1`, msg.ID).Scan(&expires); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	if left := time.Until(expires); left < 9*24*time.Hour || left > 11*24*time.Hour {
		t.Errorf("restored message lifetime = %v, want ~10 days", left)
	}
	// The held draft resumes with ~1h of review window left — NOT already
	// lapsed (which would let the TTL sweep auto-resolve it immediately).
	if err := pool.QueryRow(ctx, `SELECT approval_expires_at FROM messages WHERE id = $1`, held.ID).Scan(&approval); err != nil {
		t.Fatalf("read approval_expires_at: %v", err)
	}
	if left := time.Until(approval); left < 30*time.Minute || left > 90*time.Minute {
		t.Errorf("restored hold review window = %v, want ~1h", left)
	}
	// And the restored hold is back in the review surfaces.
	pending, err := store.ListPendingOutboundForUser(ctx, userID, 100)
	if err != nil {
		t.Fatalf("ListPendingOutboundForUser: %v", err)
	}
	found := false
	for _, p := range pending {
		if p.ID == held.ID {
			found = true
		}
	}
	if !found {
		t.Error("restored agent's hold missing from pending list")
	}
}

// TestTrashedAgentHoldsCannotBeResolved: the hold-resolution paths treat a
// trashed agent's held draft as nonexistent — no approve, no reject-scrub.
func TestTrashedAgentHoldsCannotBeResolved(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := trashTestSetup(t, store, "resolve-trash")

	held, err := store.CreatePendingOutboundMessage(ctx, agentID,
		[]string{"x@example.com"}, nil, nil, "held", "body", "", nil,
		"send", "", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	if err := store.SoftDeleteAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}

	if got, err := store.GetOutboundMessageForUser(ctx, held.ID, userID); err == nil && got != nil {
		t.Error("GetOutboundMessageForUser resolved a trashed agent's hold")
	}
	if _, _, err := store.ResolveOutboundOwner(ctx, held.ID); err == nil {
		t.Error("ResolveOutboundOwner resolved a trashed agent's hold")
	}
	if _, err := store.RejectPending(ctx, held.ID, userID, "nope"); err == nil {
		t.Error("RejectPending scrubbed a trashed agent's hold")
	}
	// The draft body is intact for restore.
	var bodyText *string
	if err := pool.QueryRow(ctx, `SELECT body_text FROM messages WHERE id = $1`, held.ID).Scan(&bodyText); err != nil {
		t.Fatalf("read body_text: %v", err)
	}
	if bodyText == nil || *bodyText != "body" {
		t.Errorf("held draft body was scrubbed while agent trashed: %v", bodyText)
	}
}

// TestLoadOutboundForSendSkipsTrash: the async send worker's load treats a
// trashed message — or a message of a trashed agent — as gone (nil, nil), so
// deleting is an effective "stop this queued send" lever.
func TestLoadOutboundForSendSkipsTrash(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := trashTestSetup(t, store, "send-trash")

	msg, err := store.CreateOutboundMessage(ctx, agentID,
		[]string{"x@example.com"}, nil, nil, "queued send", "send", "smtp", "", "", []byte("raw"))
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	// Live: the worker sees the payload.
	if p, err := store.LoadOutboundForSend(ctx, msg.ID); err != nil || p == nil {
		t.Fatalf("LoadOutboundForSend(live) = (%v, %v), want payload", p, err)
	}
	// Message in the trash → gone.
	if err := store.SoftDeleteMessage(ctx, msg.ID, agentID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	if p, err := store.LoadOutboundForSend(ctx, msg.ID); err != nil || p != nil {
		t.Fatalf("LoadOutboundForSend(trashed msg) = (%v, %v), want (nil, nil)", p, err)
	}
	// Restore the message, trash the whole AGENT → also gone.
	if err := store.RestoreMessage(ctx, msg.ID, agentID); err != nil {
		t.Fatalf("RestoreMessage: %v", err)
	}
	if err := store.SoftDeleteAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}
	if p, err := store.LoadOutboundForSend(ctx, msg.ID); err != nil || p != nil {
		t.Fatalf("LoadOutboundForSend(trashed agent) = (%v, %v), want (nil, nil)", p, err)
	}
}

// TestPurgeDeletedAgents: only trashed agents past TrashRetention are purged,
// and the purge cascades to their messages.
func TestPurgeDeletedAgents(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, staleAgent := trashTestSetup(t, store, "purge-stale")
	_, freshAgent := trashTestSetup(t, store, "purge-fresh")
	staleMsg := trashInbound(t, store, staleAgent, "bot@purge-stale.example.com", "doomed with agent")

	for _, id := range []string{staleAgent, freshAgent} {
		uid := userID
		if id == freshAgent {
			// freshAgent belongs to its own setup user; resolve it.
			a, err := store.GetAgentByID(ctx, id)
			if err != nil {
				t.Fatalf("GetAgentByID(%s): %v", id, err)
			}
			uid = a.UserID
		}
		if err := store.SoftDeleteAgent(ctx, id, uid); err != nil {
			t.Fatalf("SoftDeleteAgent(%s): %v", id, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE agent_identities SET deleted_at = now() - interval '31 days' WHERE id = $1`, staleAgent); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := store.PurgeDeletedAgents(ctx)
	if err != nil {
		t.Fatalf("PurgeDeletedAgents: %v", err)
	}
	if n < 1 {
		t.Errorf("PurgeDeletedAgents purged %d rows, want >= 1", n)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_identities WHERE id = $1`, staleAgent).Scan(&count); err != nil || count != 0 {
		t.Errorf("stale agent survived purge (count=%d, err=%v)", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id = $1`, staleMsg.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("stale agent's message survived purge (count=%d, err=%v)", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_identities WHERE id = $1`, freshAgent).Scan(&count); err != nil || count != 1 {
		t.Errorf("fresh trashed agent was purged early (count=%d, err=%v)", count, err)
	}
}

// TestTrashedAgentHoldsLeaveReviewSurfaces: a trashed agent's held messages
// disappear from the review queue, the pending list, the TTL sweep, and the
// dashboard pending count — nothing can be approved or auto-sent on behalf of
// a trashed inbox.
func TestTrashedAgentHoldsLeaveReviewSurfaces(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := trashTestSetup(t, store, "held-agent-trash")

	held, err := store.CreatePendingOutboundMessage(ctx, agentID,
		[]string{"x@example.com"}, nil, nil, "held", "body", "", nil,
		"send", "", "", "", 60)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	// Backdate the approval TTL so the hold qualifies for the expiry sweep.
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET approval_expires_at = now() - interval '10 minutes' WHERE id = $1`, held.ID); err != nil {
		t.Fatalf("backdate approval: %v", err)
	}

	if err := store.SoftDeleteAgent(ctx, agentID, userID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}

	reviews, err := store.ListReviews(ctx, userID, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	for _, r := range reviews {
		if r.ID == held.ID {
			t.Error("trashed agent's hold still in ListReviews")
		}
	}
	if _, err := store.GetReviewWithContent(ctx, userID, held.ID); err == nil {
		t.Error("GetReviewWithContent resolved a trashed agent's hold")
	}
	pending, err := store.ListPendingOutboundForUser(ctx, userID, 100)
	if err != nil {
		t.Fatalf("ListPendingOutboundForUser: %v", err)
	}
	for _, p := range pending {
		if p.ID == held.ID {
			t.Error("trashed agent's hold still in ListPendingOutboundForUser")
		}
	}
	expired, err := store.ListExpiredPending(ctx, 100)
	if err != nil {
		t.Fatalf("ListExpiredPending: %v", err)
	}
	for _, c := range expired {
		if c.MessageID == held.ID {
			t.Error("trashed agent's hold still in ListExpiredPending (TTL sweep would auto-send)")
		}
	}
}
