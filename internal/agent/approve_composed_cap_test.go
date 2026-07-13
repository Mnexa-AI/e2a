package agent_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
)

// bigAttachmentJSON builds the attachments_json for a single attachment whose
// DECODED size is decodedBytes (base64 on the wire). Shape matches
// outbound.Attachment's JSON tags (filename/content_type/data).
func bigAttachmentJSON(t *testing.T, decodedBytes int) []byte {
	t.Helper()
	att := []map[string]string{{
		"filename":     "big.bin",
		"content_type": "application/octet-stream",
		"data":         base64.StdEncoding.EncodeToString(make([]byte, decodedBytes)),
	}}
	b, err := json.Marshal(att)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	return b
}

// TestApprovePendingCore_ComposedCapOnMergedMessage closes the approve-override
// gap: the composed-message ceiling must be enforced on the MERGED message
// (reviewer edits applied on top of the stored draft), not just on the override
// fields in isolation. Here the draft carries a ~9.5 MiB attachment (under the
// 10 MiB per-attachment cap) and the reviewer overrides the body with ~1 MiB of
// text (under the 1 MiB field cap). Each field is individually legal, but the
// composed total (attachment + body) exceeds the 10 MiB SES stored-message
// ceiling, so the approve must be rejected with 413 payload_too_large and the
// hold must stay pending for the reviewer to trim.
func TestApprovePendingCore_ComposedCapOnMergedMessage(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprcompcap")

	attJSON := bigAttachmentJSON(t, 9_500_000) // ~9.5 MiB decoded, < 10 MiB per-attachment cap
	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"alice@external.test"}, nil, nil, "Held", "small body", "", attJSON, "send", "", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	bigBody := strings.Repeat("x", 1_000_000) // ~1 MiB, within the per-field cap
	ovr := agent.ApproveOverrides{BodyText: &bigBody}

	_, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, ovr, nil)
	if oerr == nil {
		t.Fatal("expected 413 payload_too_large for an over-cap merged message, got nil")
	}
	if oerr.Status != http.StatusRequestEntityTooLarge || oerr.Code != "payload_too_large" {
		t.Fatalf("want 413 payload_too_large, got status=%d code=%s msg=%q", oerr.Status, oerr.Code, oerr.Msg)
	}

	// The over-cap approve must be a no-op on the hold — never sent/accepted.
	var status, deliveryStatus string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status, COALESCE(delivery_status,'') FROM messages WHERE id=$1`, msg.ID,
		).Scan(&status, &deliveryStatus)
	}); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Errorf("status = %q, want it to remain %q after a rejected over-cap approve", status, identity.MessageStatusPendingReview)
	}
	if deliveryStatus == "accepted" {
		t.Errorf("delivery_status = accepted, want the over-cap approve to NOT enqueue delivery")
	}
}

// TestApprovePendingCore_MergedUnderCapSends guards against a false rejection:
// an edited approve whose merged composed size stays under the ceiling still
// goes through (async-accepted). Draft carries a ~5 MiB attachment; the reviewer
// overrides the body with ~1 MiB of text — merged ~6 MiB, comfortably under the
// 10 MiB cap.
func TestApprovePendingCore_MergedUnderCapSends(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprundercap")

	attJSON := bigAttachmentJSON(t, 5_000_000) // ~5 MiB decoded
	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"alice@external.test"}, nil, nil, "Held", "small body", "", attJSON, "send", "", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	bigBody := strings.Repeat("x", 1_000_000) // ~1 MiB; merged ~6 MiB < 10 MiB
	ovr := agent.ApproveOverrides{BodyText: &bigBody}

	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, ovr, nil)
	if oerr != nil {
		t.Fatalf("under-cap edited approve must succeed, got status=%d code=%s msg=%q", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.Status != identity.MessageStatusSent {
		t.Errorf("status = %q, want %q", sent.Status, identity.MessageStatusSent)
	}
}
