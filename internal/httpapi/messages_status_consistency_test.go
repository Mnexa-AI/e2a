package httpapi

import (
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// B2 (review correctness bug): the `status` field must mean the same thing on
// the list (MessageSummaryView) and detail (MessageView) for the SAME message.
// Today the summary sets status=inbox_status while the detail sets
// status=delivery_status, so an outbound message reports a different `status`
// depending on which endpoint you hit — a required field that changes value on
// re-fetch. This test pins them to agree.
func TestMessageStatusConsistentAcrossViews_Outbound(t *testing.T) {
	// An outbound message: inbox_status is never written for outbound rows (""),
	// the delivery rollup is "sent", and the HITL lifecycle is "sent".
	m := identity.Message{
		ID:             "msg_b2",
		Direction:      "outbound",
		InboxStatus:    "",
		DeliveryStatus: "sent",
		Status:         "sent",
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
	}
	summary := messageSummaryFromIdentity(m)
	detail := messageViewFromIdentity(&m)
	if summary.Status != detail.Status {
		t.Errorf(
			"status differs across views for the same outbound message: summary=%q detail=%q; "+
				"`status` must be identical on list and detail",
			summary.Status, detail.Status,
		)
	}
}

// Inbound is already consistent (the store resolves DeliveryStatus=InboxStatus
// for inbound rows) — this is a guard that the B2 fix doesn't break it.
func TestMessageStatusConsistentAcrossViews_Inbound(t *testing.T) {
	m := identity.Message{
		ID:             "msg_b2_in",
		Direction:      "inbound",
		InboxStatus:    "unread",
		DeliveryStatus: "unread", // store sets DeliveryStatus = InboxStatus for inbound
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
	}
	summary := messageSummaryFromIdentity(m)
	detail := messageViewFromIdentity(&m)
	if summary.Status != detail.Status {
		t.Errorf("inbound status differs: summary=%q detail=%q", summary.Status, detail.Status)
	}
}
