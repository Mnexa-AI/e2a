//go:build integration

package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/delivery"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// seedOutbound creates a user + verified domain + agent + one outbound message
// with the given SES provider id, marked sent to `to`. Returns userID,
// messageID, agentEmail.
func seedOutbound(t *testing.T, store *identity.Store, prefix, providerID string, to []string) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "g-"+prefix)
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
	agentEmail := "bot@" + domain
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	msg, err := store.CreateOutboundMessage(ctx, agentEmail, to, nil, nil, "Subj", "send", "smtp", providerID, "", nil)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	if err := store.MarkMessageSent(ctx, msg.ID, "relay", to, nil, nil); err != nil {
		t.Fatalf("MarkMessageSent: %v", err)
	}
	return user.ID, msg.ID, agentEmail
}

func deliveryStatus(t *testing.T, store *identity.Store, messageID, agentEmail string) string {
	t.Helper()
	msg, err := store.GetMessageWithContent(context.Background(), messageID, agentEmail)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	return msg.DeliveryStatus
}

func TestDeliveryPipeline_DeliveredThenBounceSuppresses(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	userID, msgID, agentEmail := seedOutbound(t, store, "dlv", "ses-msg-1", []string{"a@x.com"})

	// Initial rollup is 'sent'.
	if got := deliveryStatus(t, store, msgID, agentEmail); got != "sent" {
		t.Fatalf("initial delivery_status=%q, want sent", got)
	}

	consumer := delivery.NewConsumer(store, nil)

	// Delivery → delivered.
	if err := consumer.Process(ctx, &delivery.Event{
		Kind: delivery.KindDelivery, SESMessageID: "ses-msg-1",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusDelivered}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := deliveryStatus(t, store, msgID, agentEmail); got != "delivered" {
		t.Fatalf("after delivery: delivery_status=%q, want delivered", got)
	}

	// A later, lower-rank deferred must NOT regress a delivered rollup (monotonic).
	_ = consumer.Process(ctx, &delivery.Event{
		Kind: delivery.KindDeliveryDelay, SESMessageID: "ses-msg-1",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusDeferred}},
	})
	if got := deliveryStatus(t, store, msgID, agentEmail); got != "delivered" {
		t.Fatalf("monotonic violated: delivery_status=%q, want still delivered", got)
	}

	// A hard bounce wins over delivered AND suppresses the address.
	if err := consumer.Process(ctx, &delivery.Event{
		Kind: delivery.KindBounce, SESMessageID: "ses-msg-1",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusBounced, Detail: "550", Suppress: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := deliveryStatus(t, store, msgID, agentEmail); got != "bounced" {
		t.Fatalf("after bounce: delivery_status=%q, want bounced", got)
	}
	supp, err := store.SuppressedAddresses(ctx, userID, []string{"a@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(supp) != 1 || supp[0] != "a@x.com" {
		t.Fatalf("expected a@x.com suppressed, got %v", supp)
	}
}

func TestDeliveryPipeline_PerRecipientRollup(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	_, msgID, agentEmail := seedOutbound(t, store, "multi", "ses-msg-2", []string{"good@x.com", "bad@x.com"})
	consumer := delivery.NewConsumer(store, nil)

	// good delivers, bad bounces → rollup is the worst (bounced).
	_ = consumer.Process(ctx, &delivery.Event{Kind: delivery.KindDelivery, SESMessageID: "ses-msg-2",
		Recipients: []delivery.RecipientOutcome{{Address: "good@x.com", Status: delivery.StatusDelivered}}})
	_ = consumer.Process(ctx, &delivery.Event{Kind: delivery.KindBounce, SESMessageID: "ses-msg-2",
		Recipients: []delivery.RecipientOutcome{{Address: "bad@x.com", Status: delivery.StatusBounced, Suppress: true}}})

	if got := deliveryStatus(t, store, msgID, agentEmail); got != "bounced" {
		t.Fatalf("rollup=%q, want bounced (worst across recipients)", got)
	}
}

func TestSuppressionCRUD(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)
	user, _ := store.CreateOrGetUser(ctx, "supp@example.com", "S", "g-supp")

	added, err := store.AddSuppression(ctx, user.ID, "X@x.com", "manual block", "manual", "")
	if err != nil || !added {
		t.Fatalf("AddSuppression added=%v err=%v", added, err)
	}
	// Idempotent: second add returns added=false.
	added2, _ := store.AddSuppression(ctx, user.ID, "x@x.com", "again", "manual", "")
	if added2 {
		t.Fatal("re-adding the same (normalized) address should return added=false")
	}
	list, _ := store.ListSuppressions(ctx, user.ID, 50, time.Time{}, "")
	if len(list) != 1 || list[0].Address != "x@x.com" {
		t.Fatalf("list=%v", list)
	}
	found, _ := store.RemoveSuppression(ctx, user.ID, "x@x.com")
	if !found {
		t.Fatal("RemoveSuppression should report found")
	}
	if supp, _ := store.SuppressedAddresses(ctx, user.ID, []string{"x@x.com"}); len(supp) != 0 {
		t.Fatalf("address should be un-suppressed, got %v", supp)
	}
}

// TestCorrelationMatchesBracketedSESID pins the review BLOCKER: SES stores the
// provider id angle-bracketed (and sometimes @region.amazonses.com-suffixed),
// but the SNS notification carries the BARE id. Correlation must match across
// those shapes — an exact-equality match would silently drop all feedback.
func TestCorrelationMatchesBracketedSESID(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	// Store the realistic bracketed+suffixed shape; correlate with the bare id.
	_, msgID, agentEmail := seedOutbound(t, store, "corr", "<010f0193abc-000000@us-east-2.amazonses.com>", []string{"a@x.com"})

	mID, _, _, _, found, err := store.CorrelateBySESMessageID(ctx, "010f0193abc-000000")
	if err != nil {
		t.Fatal(err)
	}
	if !found || mID != msgID {
		t.Fatalf("bare-id correlation against bracketed stored id failed: found=%v id=%q want %q", found, mID, msgID)
	}

	// And the full pipeline must transition delivery_status via the bare id.
	if err := delivery.NewConsumer(store, nil).Process(ctx, &delivery.Event{
		Kind: delivery.KindDelivery, SESMessageID: "010f0193abc-000000",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusDelivered}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := deliveryStatus(t, store, msgID, agentEmail); got != "delivered" {
		t.Fatalf("delivery_status=%q, want delivered (bare-id correlation)", got)
	}
}

// TestConcurrentRollupMonotonic pins the review race fix: concurrent SES events
// for the same recipient (one delivered, one bounced) must converge to the
// worst status (bounced) — the message-row lock serializes the merge.
func TestConcurrentRollupMonotonic(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)
	consumer := delivery.NewConsumer(store, nil)

	// Seed the user/domain/agent once; create a distinct message per iteration.
	_, _, agentEmail := seedOutbound(t, store, "race", "ses-race-seed", []string{"a@x.com"})

	for i := 0; i < 25; i++ {
		providerID := "ses-race-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		msg, err := store.CreateOutboundMessage(ctx, agentEmail, []string{"a@x.com"}, nil, nil, "S", "send", "smtp", providerID, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMessageSent(ctx, msg.ID, "relay", []string{"a@x.com"}, nil, nil); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 2)
		go func() {
			done <- consumer.Process(ctx, &delivery.Event{Kind: delivery.KindDelivery, SESMessageID: providerID,
				Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusDelivered}}})
		}()
		go func() {
			done <- consumer.Process(ctx, &delivery.Event{Kind: delivery.KindBounce, SESMessageID: providerID,
				Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusBounced, Detail: "550"}}})
		}()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := deliveryStatus(t, store, msg.ID, agentEmail); got != "bounced" {
			t.Fatalf("iter %d: rollup=%q, want bounced (concurrent monotonic)", i, got)
		}
	}
}

// TestDetailNotClobberedByLaterEvent pins the review low fix: a later
// lower-rank event carrying a detail must not overwrite the terminal
// diagnostic.
func TestDetailNotClobberedByLaterEvent(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)
	_, msgID, _ := seedOutbound(t, store, "detail", "ses-detail", []string{"a@x.com"})
	consumer := delivery.NewConsumer(store, nil)

	_ = consumer.Process(ctx, &delivery.Event{Kind: delivery.KindBounce, SESMessageID: "ses-detail",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusBounced, Detail: "550 mailbox full"}}})
	// A late delivered (lower rank) with its own detail must not clobber.
	_ = consumer.Process(ctx, &delivery.Event{Kind: delivery.KindDelivery, SESMessageID: "ses-detail",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusDelivered, Detail: "ok"}}})

	var detail string
	if err := pool.QueryRow(ctx, "SELECT COALESCE(detail,'') FROM message_recipients WHERE message_id=$1 AND address='a@x.com'", msgID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "550 mailbox full" {
		t.Fatalf("detail=%q, want the preserved bounce reason", detail)
	}
}
