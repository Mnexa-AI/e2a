package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

// TestEmitBlockedOutbound_Integration_SurvivesMessageIDFK drives the exact
// event emitBlockedOutbound builds through the real outbox into the
// production schema. A blocked send is rowless — its msgblk_ soft-ref must
// never be written to the webhook_events.message_id column, whose FK to
// messages(id) would reject it and roll back (= drop) the event. That drop
// shipped silently because the unit tests never crossed the FK; this test
// exists so it can't come back.
func TestEmitBlockedOutbound_Integration_SurvivesMessageIDFK(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	ids := identity.NewStore(pool)
	user, err := ids.CreateOrGetUser(ctx, "blocked-fk@example.com", "Blocked FK", "blocked-fk-subject")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ag := &identity.AgentIdentity{ID: "blocked-bot@x.example.com", Domain: "x.example.com", UserID: user.ID}
	req := outbound.SendRequest{To: []string{"alice@evil.com"}, Subject: "blocked by gate", Body: "refused"}
	e, softRef := agent.BuildBlockedOutboundEventForTest(ag, req, "recipient_gate", "recipient not in allowlist")

	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := outbox.PublishTx(ctx, tx, e); err != nil {
		t.Fatalf("PublishTx: %v (a 23503 here means the msgblk_ soft-ref hit the messages FK again)", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var colMessageID *string
	var dataMessageID string
	err = pool.QueryRow(ctx,
		`SELECT message_id, envelope->'data'->>'message_id'
		   FROM webhook_events WHERE id = $1 AND type = $2`,
		e.ID, webhookpub.EventEmailBlocked,
	).Scan(&colMessageID, &dataMessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("email.blocked event %s not in webhook_events — dropped", e.ID)
	}
	if err != nil {
		t.Fatalf("query webhook_events: %v", err)
	}
	if colMessageID != nil {
		t.Errorf("message_id column = %q, want NULL (rowless block must not reference messages)", *colMessageID)
	}
	if dataMessageID != softRef {
		t.Errorf("envelope data.message_id = %q, want soft-ref %q", dataMessageID, softRef)
	}
}
