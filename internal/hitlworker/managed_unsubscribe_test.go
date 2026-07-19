package hitlworker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
)

type ttlUnsubscribeIssuer struct {
	calls     int
	recipient string
}

func (i *ttlUnsubscribeIssuer) Issue(_ context.Context, _, _, recipient string) (string, error) {
	i.calls++
	i.recipient = recipient
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
