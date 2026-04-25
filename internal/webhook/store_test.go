package webhook_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

func setupDeliveryFixture(t *testing.T) (*pgxpool.Pool, *identity.Store, *webhook.DeliveryStore, *identity.AgentIdentity, *identity.Message) {
	t.Helper()

	pool := testutil.TestDB(t)
	identityStore := identity.NewStore(pool)
	deliveryStore := webhook.NewDeliveryStore(pool)
	ctx := context.Background()

	user, err := identityStore.CreateOrGetUser(ctx, "retry@test.com", "Retry User", "retry-google-sub")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := identityStore.ClaimOrCreateDomain(ctx, "retry.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	agent, err := identityStore.CreateAgent(ctx, "bot@retry.example.com", "retry.example.com", "", "https://example.com/webhook", "cloud", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	msg, err := identityStore.CreateOutboundMessage(ctx, agent.ID, []string{"alice@example.com"}, nil, nil, "Hello", "send", "webhook", "", "")
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}

	return pool, identityStore, deliveryStore, agent, msg
}

func TestCreateDeliveryAndGetPendingDeliveriesUsesMessageKeyedSchema(t *testing.T) {
	_, _, deliveryStore, agent, msg := setupDeliveryFixture(t)
	ctx := context.Background()

	delivery, err := deliveryStore.CreateDelivery(ctx, msg.ID, "initial failure")
	if err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}

	if delivery.MessageID != msg.ID {
		t.Fatalf("MessageID = %q, want %q", delivery.MessageID, msg.ID)
	}

	pending, err := deliveryStore.GetPendingDeliveries(ctx, 10)
	if err != nil {
		t.Fatalf("GetPendingDeliveries: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending deliveries = %d, want 1", len(pending))
	}

	got := pending[0]
	if got.MessageID != msg.ID {
		t.Fatalf("pending MessageID = %q, want %q", got.MessageID, msg.ID)
	}
	if got.AgentID != agent.ID {
		t.Fatalf("pending AgentID = %q, want %q", got.AgentID, agent.ID)
	}
	if got.LastError != "initial failure" {
		t.Fatalf("pending LastError = %q, want %q", got.LastError, "initial failure")
	}
}

func TestDeliveryStatusUpdatesByMessageID(t *testing.T) {
	_, identityStore, deliveryStore, _, msg := setupDeliveryFixture(t)
	ctx := context.Background()

	if _, err := deliveryStore.CreateDelivery(ctx, msg.ID, ""); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}

	retryAt := time.Now().Add(5 * time.Minute).UTC().Round(time.Second)
	if err := deliveryStore.MarkAttemptFailed(ctx, msg.ID, "temporary failure", retryAt); err != nil {
		t.Fatalf("MarkAttemptFailed: %v", err)
	}

	activity, err := identityStore.ListActivityByAgent(ctx, msg.AgentID, 10)
	if err != nil {
		t.Fatalf("ListActivityByAgent after failed attempt: %v", err)
	}
	if len(activity) != 1 {
		t.Fatalf("activity len after failed attempt = %d, want 1", len(activity))
	}
	if activity[0].WebhookStatus != "pending" {
		t.Fatalf("WebhookStatus after failed attempt = %q, want pending", activity[0].WebhookStatus)
	}
	if activity[0].WebhookAttempts != 1 {
		t.Fatalf("WebhookAttempts after failed attempt = %d, want 1", activity[0].WebhookAttempts)
	}
	if activity[0].WebhookError != "temporary failure" {
		t.Fatalf("WebhookError after failed attempt = %q, want temporary failure", activity[0].WebhookError)
	}

	if err := deliveryStore.MarkDelivered(ctx, msg.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	activity, err = identityStore.ListActivityByAgent(ctx, msg.AgentID, 10)
	if err != nil {
		t.Fatalf("ListActivityByAgent: %v", err)
	}
	if len(activity) != 1 {
		t.Fatalf("activity len = %d, want 1", len(activity))
	}
	if activity[0].WebhookStatus != "delivered" {
		t.Fatalf("WebhookStatus = %q, want delivered", activity[0].WebhookStatus)
	}
	if activity[0].WebhookAttempts != 2 {
		t.Fatalf("WebhookAttempts = %d, want 2", activity[0].WebhookAttempts)
	}
}
