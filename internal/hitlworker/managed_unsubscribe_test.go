package hitlworker_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outbound"
)

type ttlUnsubscribeIssuer struct {
	calls     int
	recipient string
	err       error
}

func (i *ttlUnsubscribeIssuer) Issue(_ context.Context, _, _, recipient string) (string, error) {
	i.calls++
	i.recipient = recipient
	if i.err != nil {
		return "", i.err
	}
	return "https://api.example/u/u1_ttl", nil
}

func TestWorkerAutoApproveManagedUnsubscribeMintsAtFinalization(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	ctx := context.Background()
	ag := prepareAgent(t, store, "ttl-managed", identity.HITLExpirationApprove)
	issuer := &ttlUnsubscribeIssuer{}
	w.SetManagedUnsubscribeIssuer(issuer)
	msg, err := store.CreatePendingOutboundMessageManaged(ctx, ag.ID, []string{"FINAL@Example.net"}, nil, nil, "managed ttl", "body", "<p>html</p>", nil, "send", "", "", "", 60, true)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	w.RunOnce(ctx)
	if issuer.calls != 1 || issuer.recipient != "final@example.net" {
		t.Fatalf("issuer=%+v", issuer)
	}
	var raw []byte
	var status string
	if err := pool.QueryRow(ctx, `SELECT raw_message, status FROM messages WHERE id=$1`, msg.ID).Scan(&raw, &status); err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredApproved || !strings.Contains(string(raw), "List-Unsubscribe: <https://api.example/u/u1_ttl>") {
		t.Fatalf("status=%s raw=%s", status, raw)
	}
}

func TestWorkerAutoApproveManagedUnsubscribeRejectsWhenFooterCrossesCap(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()
	ag := prepareAgent(t, store, "ttl-managed-cap", identity.HITLExpirationApprove)
	w.SetManagedUnsubscribeIssuer(&ttlUnsubscribeIssuer{})
	subject := "s"
	body := strings.Repeat("x", outbound.MaxComposedMessageBytes-len(subject))
	msg, err := store.CreatePendingOutboundMessageManaged(ctx, ag.ID,
		[]string{"final@example.net"}, nil, nil, subject, body, "", nil,
		"send", "", "", "", 60, true)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	w.RunOnce(ctx)
	var status, deliveryStatus string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(delivery_status, '') FROM messages WHERE id=$1`, msg.ID).Scan(&status, &deliveryStatus); err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusReviewExpiredRejected || deliveryStatus == "accepted" {
		t.Fatalf("status=%q delivery_status=%q", status, deliveryStatus)
	}
	if got := smtpDone(); len(got) != 0 {
		t.Fatalf("sent %d over-cap messages", len(got))
	}
}

func TestWorkerAutoApproveManagedUnsubscribeLeavesPendingWithoutIssuer(t *testing.T) {
	testTTLManagedUnsubscribeIssuerFailure(t, nil)
}

func TestWorkerAutoApproveManagedUnsubscribeLeavesPendingOnIssueError(t *testing.T) {
	testTTLManagedUnsubscribeIssuerFailure(t, &ttlUnsubscribeIssuer{err: errors.New("token store unavailable")})
}

func testTTLManagedUnsubscribeIssuerFailure(t *testing.T, issuer *ttlUnsubscribeIssuer) {
	t.Helper()
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()
	ag := prepareAgent(t, store, "ttl-managed-issuer-failure", identity.HITLExpirationApprove)
	if issuer != nil {
		w.SetManagedUnsubscribeIssuer(issuer)
	}
	msg, err := store.CreatePendingOutboundMessageManaged(ctx, ag.ID,
		[]string{"final@example.net"}, nil, nil, "managed ttl", "body", "", nil,
		"send", "", "", "", 60, true)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	w.RunOnce(ctx)
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, msg.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Fatalf("status=%q, want pending_review", status)
	}
	if got := smtpDone(); len(got) != 0 {
		t.Fatalf("sent %d messages despite unavailable issuer", len(got))
	}
}
