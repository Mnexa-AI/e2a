package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func seedHeldAgent(t *testing.T, store *identity.Store, ctx context.Context, domain string) string {
	t.Helper()
	user, err := store.CreateOrGetUser(ctx, "o@"+domain, "O", "g-"+domain)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return ag.ID
}

// TestHeldMessage_MutationGuard: an agent that learns a held message's ID (it
// receives it in the email.injection_detected webhook) must NOT be able to mutate
// or oracle it via the label/inbox-status paths — they're held, so they 404.
func TestHeldMessage_MutationGuard(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := seedHeldAgent(t, store, ctx, "heldguard.example.com")

	held, err := store.CreateInboundMessage(ctx, "", agentID, "evil@x.com", "bot@heldguard.example.com",
		"<held@x>", "subj", "", "", []byte("raw"), nil, nil, false, "", nil, nil, nil,
		identity.InboundScreening{Status: identity.MessageStatusPendingReview, ReviewReason: "inbound_scan"})
	if err != nil {
		t.Fatalf("CreateInboundMessage(held): %v", err)
	}
	normal, err := store.CreateInboundMessage(ctx, "", agentID, "alice@x.com", "bot@heldguard.example.com",
		"<ok@x>", "hi", "", "", []byte("raw"), nil, nil, false, "", nil, nil, nil,
		identity.InboundScreening{})
	if err != nil {
		t.Fatalf("CreateInboundMessage(normal): %v", err)
	}

	// ModifyMessageLabels on a held message → ErrMessageNotFound (no oracle, no mutation).
	if _, err := store.ModifyMessageLabels(ctx, held.ID, agentID, []string{"x"}, nil); !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("ModifyMessageLabels(held) = %v, want ErrMessageNotFound", err)
	}
	// Control: the same call on a delivered message works.
	if _, err := store.ModifyMessageLabels(ctx, normal.ID, agentID, []string{"x"}, nil); err != nil {
		t.Errorf("ModifyMessageLabels(normal) = %v, want nil", err)
	}

	// UpdateMessageDeliveryStatus on a held message must not flip inbox_status.
	if err := store.UpdateMessageDeliveryStatus(ctx, held.ID, agentID, "read"); err != nil {
		t.Fatalf("UpdateMessageDeliveryStatus: %v", err)
	}
	var inbox string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(inbox_status,'') FROM messages WHERE id=$1`, held.ID).Scan(&inbox)
	if inbox == "read" {
		t.Errorf("held message inbox_status was mutated to %q (should be guarded)", inbox)
	}
}

// TestValidateScanConfig_ThresholdLadder: equal/inverted thresholds are rejected so
// a (0,0) PATCH can't collapse the review band into block-everything.
func TestValidateScanConfig_ThresholdLadder(t *testing.T) {
	base := identity.ScanConfig{
		InboundPolicyAction: identity.ScanActionFlag,
		OutboundPolicy:      identity.OutboundPolicyOpen, OutboundPolicyAction: identity.ScanActionFlag,
		InboundScan: identity.ScanOn, OutboundScan: identity.ScanOn,
		InboundScanReviewThreshold: 0.5, InboundScanBlockThreshold: 0.9,
		OutboundScanReviewThreshold: 0.5, OutboundScanBlockThreshold: 0.9,
	}
	if err := identity.ValidateScanConfig(base); err != nil {
		t.Fatalf("valid ladder rejected: %v", err)
	}
	for _, tc := range []struct {
		name          string
		review, block float64
	}{
		{"equal zero", 0, 0},
		{"equal mid", 0.5, 0.5},
		{"inverted", 0.9, 0.2},
	} {
		c := base
		c.OutboundScanReviewThreshold, c.OutboundScanBlockThreshold = tc.review, tc.block
		if err := identity.ValidateScanConfig(c); err == nil {
			t.Errorf("%s: (%v,%v) accepted, want rejected", tc.name, tc.review, tc.block)
		}
	}
}
