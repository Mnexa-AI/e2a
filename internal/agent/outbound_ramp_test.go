package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outboundsend"
	"github.com/tokencanopy/e2a/internal/sendramp"
	"github.com/tokencanopy/e2a/internal/testutil"
)

func seedOutboundRampAdapter(t *testing.T, suffix string) (*sendramp.Store, string, string, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	ctx := context.Background()
	ids := identity.NewStore(pool)
	user, err := ids.CreateOrGetUser(ctx, "adapter-"+suffix+"@example.com", "Adapter", "adapter-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	domain := "adapter-" + suffix + ".example.com"
	if _, err := ids.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET sending_status='verified' WHERE domain=$1`, domain); err != nil {
		t.Fatal(err)
	}
	ag, err := ids.CreateAgent(ctx, "agent@"+domain, domain, "", "", "local", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := ids.CreateOutboundMessage(ctx, ag.ID, []string{"one@example.net"}, nil, nil, "subject", "send", "smtp", "", "", []byte("raw"))
	if err != nil {
		t.Fatal(err)
	}
	return sendramp.NewStore(pool), user.ID, domain, msg.ID
}

func TestOutboundRampGateDisabledPersistsExemption(t *testing.T) {
	store, userID, domain, messageID := seedOutboundRampAdapter(t, "disabled")
	gate := agent.NewOutboundRampGate(store, sendramp.DefaultSchedule, false)
	d, err := gate.Reserve(context.Background(), outboundsend.RampRequest{MessageID: messageID, UserID: userID, Domain: domain, Units: 1})
	if err != nil || !d.Allowed {
		t.Fatalf("Reserve = %+v, %v", d, err)
	}
	snap, err := store.Snapshot(context.Background(), userID, domain, time.Now())
	if err != nil || snap.Status != sendramp.StatusExempt {
		t.Fatalf("Snapshot = %+v, %v", snap, err)
	}
}

func TestOutboundRampGateInjectsDayAndDelegatesLifecycle(t *testing.T) {
	store, userID, domain, messageID := seedOutboundRampAdapter(t, "enabled")
	day := time.Date(2026, 7, 2, 23, 30, 0, 0, time.FixedZone("west", -7*60*60))
	gate := agent.NewOutboundRampGate(store, sendramp.NewSchedule(50, 100, 2), true, func() time.Time { return day })
	d, err := gate.Reserve(context.Background(), outboundsend.RampRequest{MessageID: messageID, UserID: userID, Domain: domain, Units: 25})
	if err != nil || !d.Allowed {
		t.Fatalf("Reserve = %+v, %v", d, err)
	}
	if err := gate.Confirm(context.Background(), messageID); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Snapshot(context.Background(), userID, domain, day)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ActiveDays != 1 || snap.UsedToday != 25 {
		t.Fatalf("Snapshot = %+v", snap)
	}
}
