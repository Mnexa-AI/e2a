package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestCreateInboundMessage_PersistsScreening proves the denormalized screening
// verdict (review_reason/scan_score/scan_action, migration 037) round-trips through
// the inbound INSERT path the relay uses.
func TestCreateInboundMessage_PersistsScreening(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "o@scr.example.com", "O", "g-scr")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "scr.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "bot@scr.example.com", "scr.example.com", "", "", "", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	score := 0.83
	msg, err := store.CreateInboundMessage(ctx, "", "bot@scr.example.com", "alice@evil.com", "bot@scr.example.com",
		"", "Hi", "", "unread", []byte("Subject: Hi\r\n\r\nx"), nil, nil, false, "",
		[]string{"bot@scr.example.com"}, nil, nil,
		identity.InboundScreening{ReviewReason: identity.ReviewReasonInboundScan, ScanScore: &score, ScanAction: "review"})
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	var rr, sa string
	var ss *float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(review_reason,''), scan_score, COALESCE(scan_action,'') FROM messages WHERE id=$1`,
		msg.ID,
	).Scan(&rr, &ss, &sa); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if rr != identity.ReviewReasonInboundScan {
		t.Errorf("review_reason = %q, want inbound_scan", rr)
	}
	if sa != "review" {
		t.Errorf("scan_action = %q, want review", sa)
	}
	if ss == nil || *ss < 0.82 || *ss > 0.84 {
		t.Errorf("scan_score = %v, want ~0.83", ss)
	}

	// A message with no screening leaves the columns NULL (→ empty / nil).
	plain, err := store.CreateInboundMessage(ctx, "", "bot@scr.example.com", "friend@acme.com", "bot@scr.example.com",
		"", "Hi", "", "unread", []byte("Subject: Hi\r\n\r\nx"), nil, nil, false, "",
		[]string{"bot@scr.example.com"}, nil, nil, identity.InboundScreening{})
	if err != nil {
		t.Fatalf("CreateInboundMessage(plain): %v", err)
	}
	var prr, psa string
	var pss *float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(review_reason,''), scan_score, COALESCE(scan_action,'') FROM messages WHERE id=$1`,
		plain.ID,
	).Scan(&prr, &pss, &psa); err != nil {
		t.Fatalf("read back plain: %v", err)
	}
	if prr != "" || psa != "" || pss != nil {
		t.Errorf("unscreened message should leave screening columns NULL, got rr=%q sa=%q ss=%v", prr, psa, pss)
	}
}
