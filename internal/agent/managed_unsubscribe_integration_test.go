package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/outbound"
)

type recordingIssuer struct {
	calls     int
	recipient string
	err       error
}

func (i *recordingIssuer) Issue(_ context.Context, _, _, recipient string) (string, error) {
	i.calls++
	i.recipient = recipient
	if i.err != nil {
		return "", i.err
	}
	return "https://api.example/u/u1_stable", nil
}

func TestDeliverOutboundManagedUnsubscribeBindsBeforeAccept(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "manageddirect")
	issuer := &recordingIssuer{}
	api.SetManagedUnsubscribeIssuer(issuer)
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"Person <USER@Example.net>", "user@example.net"}, Subject: "managed", Body: "body",
		Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"},
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatal(oerr)
	}
	if issuer.calls != 1 || issuer.recipient != "user@example.net" {
		t.Fatalf("issuer=%+v", issuer)
	}
	var raw []byte
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT raw_message FROM messages WHERE id=$1`, res.MessageID).Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "List-Unsubscribe: <https://api.example/u/u1_stable>") || !strings.Contains(string(raw), "Unsubscribe from emails sent by "+ag.ID) {
		t.Fatalf("accepted raw missing managed unsubscribe:\n%s", raw)
	}
}

func TestDeliverOutboundManagedUnsubscribeIssuerFailureDoesNotAccept(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "managedfail")
	api.SetManagedUnsubscribeIssuer(&recordingIssuer{err: errors.New("store down")})
	_, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"user@example.net"}, Subject: "must not accept", Body: "body", Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"},
	}, "send", "", nil, nil)
	if oerr == nil || oerr.Status != 500 {
		t.Fatalf("error=%+v", oerr)
	}
	var count int
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1 AND subject='must not accept'`, ag.ID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("issuer failure accepted %d messages", count)
	}
}

func TestHITLManagedUnsubscribePersistsIntentThenMintsOnApproval(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "managedhitl")
	issuer := &recordingIssuer{}
	api.SetManagedUnsubscribeIssuer(issuer)
	held, err := api.HoldForApprovalCore(ctx, ag, outbound.SendRequest{
		To: []string{"original@example.net"}, Subject: "held managed", Body: "body", Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"},
	}, "send", "")
	if err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 0 {
		t.Fatalf("hold minted token: %d", issuer.calls)
	}
	draft, err := store.GetOutboundMessageForUser(ctx, held.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !draft.ManagedUnsubscribe {
		t.Fatal("managed intent was not persisted")
	}
	override := []string{"FINAL@Example.net"}
	approved, oerr := api.ApprovePendingCore(ctx, user.ID, held.ID, ag.ID, agent.ApproveOverrides{To: &override}, nil)
	if oerr != nil {
		t.Fatal(oerr)
	}
	if issuer.calls != 1 || issuer.recipient != "final@example.net" {
		t.Fatalf("issuer=%+v", issuer)
	}
	if approved.DeliveryStatus != "accepted" {
		t.Fatalf("approved=%+v", approved)
	}
	var raw []byte
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT raw_message FROM messages WHERE id=$1`, held.ID).Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "List-Unsubscribe: <https://api.example/u/u1_stable>") {
		t.Fatalf("raw=%s", raw)
	}
}
