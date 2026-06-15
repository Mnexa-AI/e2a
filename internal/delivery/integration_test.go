//go:build integration

package delivery_test

import (
	"context"
	"testing"

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
	msg, err := store.CreateOutboundMessage(ctx, agentEmail, to, nil, nil, "Subj", "send", "smtp", providerID, "")
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
	list, _ := store.ListSuppressions(ctx, user.ID)
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
