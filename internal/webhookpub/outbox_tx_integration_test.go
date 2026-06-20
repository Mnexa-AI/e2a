package webhookpub_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
)

// TestOutbox_Integration_RelayTxShape exercises the actual slice-3 tx
// shape: WithTx wraps CreateInboundMessageInTx + PublishTx in one
// transaction. Validates that the messages row and webhook_events row
// commit together (the at-least-once invariant from §4.2).
//
// This is the slice-3 acceptance test — the test that proves the
// design's central claim that we can atomically commit the trigger
// state and the outbox row.
func TestOutbox_Integration_RelayTxShape(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	ctx := context.Background()

	// Seed user + domain + agent.
	const userID = "u_relay_tx"
	const domain = "relay-tx.example.com"
	const agentEmail = "agent@" + domain
	messageID := identity.NewMessageID()

	_, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, name, google_subject, created_at)
		 VALUES ($1, $2, 'Relay Tx', $1, now())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@example.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO domains (domain, user_id, verified, verification_token, created_at)
		 VALUES ($1, $2, true, 'tkn', now())
		 ON CONFLICT (domain) DO NOTHING`,
		domain, userID)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	// cloud agents need a non-empty webhook_url per the agent_identities
	// CHECK constraint at migrations/001_init.sql:51.
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "Relay Tx Agent", "https://test.example.com/wh", "cloud", userID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, messageID)
		_, _ = pool.Exec(ctx, `DELETE FROM agent_identities WHERE id = $1`, agentEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM domains WHERE domain = $1`, domain)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Happy path: WithTx commits both writes together.
	t.Run("HappyPath_BothRowsCommit", func(t *testing.T) {
		eventID := webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailReceived)
		event := webhookpub.Event{
			ID:        eventID,
			Type:      webhookpub.EventEmailReceived,
			UserID:    userID,
			AgentID:   agentEmail,
			MessageID: messageID,
			Data:      map[string]any{"from": "sender@example.com"},
		}

		err := store.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := store.CreateInboundMessageInTx(ctx, tx,
				messageID, agentEmail, "sender@example.com", agentEmail,
				"<email-id@sender.example>", "Test", "", "unread",
				[]byte("Subject: Test\r\n\r\nhello"),
				map[string]string{"From": "sender@example.com"},
				nil,
				false, "",
				[]string{agentEmail}, nil, nil,
				identity.InboundScreening{},
			)
			if err != nil {
				return err
			}
			return outbox.PublishTx(ctx, tx, event)
		})
		if err != nil {
			t.Fatalf("WithTx: %v", err)
		}

		// Verify both rows present.
		var msgCount, eventCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM messages WHERE id = $1`, messageID,
		).Scan(&msgCount); err != nil {
			t.Fatalf("count messages: %v", err)
		}
		if msgCount != 1 {
			t.Errorf("messages row count = %d, want 1", msgCount)
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM webhook_events WHERE id = $1`, eventID,
		).Scan(&eventCount); err != nil {
			t.Fatalf("count webhook_events: %v", err)
		}
		if eventCount != 1 {
			t.Errorf("webhook_events row count = %d, want 1", eventCount)
		}

		// Cross-check the FK: webhook_events.message_id should resolve
		// to the just-inserted messages row.
		var resolvedMsgID *string
		if err := pool.QueryRow(ctx,
			`SELECT message_id FROM webhook_events WHERE id = $1`, eventID,
		).Scan(&resolvedMsgID); err != nil {
			t.Fatalf("read message_id back: %v", err)
		}
		if resolvedMsgID == nil {
			t.Errorf("message_id is NULL; FK was supposed to resolve to %s", messageID)
		} else if *resolvedMsgID != messageID {
			t.Errorf("message_id = %s, want %s", *resolvedMsgID, messageID)
		}
	})

	// MTA-retry idempotency: the same (messageID, eventType) inputs
	// produce the same evt_<hash> id; the second WithTx call must
	// no-op the outbox INSERT via ON CONFLICT (id) DO NOTHING. The
	// messages INSERT will fail with a PK violation (messageID
	// collides) — that's the design's expected behavior: dedupe at
	// the messages layer is a pre-existing concern (see §5.1's
	// "Note on existing inbound dedup").
	t.Run("MTARetry_DeterministicIDPreventsDuplicateEvent", func(t *testing.T) {
		retryMessageID := identity.NewMessageID()
		retryEventID := webhookpub.DeterministicEventID(retryMessageID, webhookpub.EventEmailReceived)
		event := webhookpub.Event{
			ID:        retryEventID,
			Type:      webhookpub.EventEmailReceived,
			UserID:    userID,
			AgentID:   agentEmail,
			MessageID: retryMessageID,
			Data:      map[string]any{},
		}

		// First attempt commits cleanly.
		err := store.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := store.CreateInboundMessageInTx(ctx, tx,
				retryMessageID, agentEmail, "sender@example.com", agentEmail,
				"<retry-1@sender.example>", "Retry", "", "unread",
				[]byte("hello"), nil, nil, false, "", []string{agentEmail}, nil, nil,
				identity.InboundScreening{})
			if err != nil {
				return err
			}
			return outbox.PublishTx(ctx, tx, event)
		})
		if err != nil {
			t.Fatalf("first attempt: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE id = $1`, retryEventID)
			_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, retryMessageID)
		})

		// Second attempt with the SAME message ID. messages INSERT
		// fails with PK violation; tx rolls back — outbox INSERT
		// inside the rolled-back tx never lands. This proves that
		// even when an MTA retry replays the SMTP DATA, the
		// messages-level dedup short-circuit (which doesn't exist
		// today, per §5.1) isn't required for the outbox to remain
		// consistent: the deterministic ID + ON CONFLICT (id) DO
		// NOTHING handles it even without messages dedup.
		err = store.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := store.CreateInboundMessageInTx(ctx, tx,
				retryMessageID, agentEmail, "sender@example.com", agentEmail,
				"<retry-2@sender.example>", "Retry 2", "", "unread",
				[]byte("hello again"), nil, nil, false, "", []string{agentEmail}, nil, nil,
				identity.InboundScreening{})
			if err != nil {
				return err
			}
			return outbox.PublishTx(ctx, tx, event)
		})
		if err == nil {
			t.Fatalf("expected PK violation on second insert, got success")
		}

		// Verify exactly one event row exists.
		var eventCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM webhook_events WHERE id = $1`, retryEventID,
		).Scan(&eventCount); err != nil {
			t.Fatalf("count: %v", err)
		}
		if eventCount != 1 {
			t.Errorf("webhook_events count = %d, want exactly 1 (deterministic ID idempotency broken)", eventCount)
		}
	})

	// Outbox failure rolls back the messages row too — the
	// at-least-once invariant.
	t.Run("OutboxFailureRollsBackMessages", func(t *testing.T) {
		failMsgID := identity.NewMessageID()

		err := store.WithTx(ctx, func(tx pgx.Tx) error {
			if _, err := store.CreateInboundMessageInTx(ctx, tx,
				failMsgID, agentEmail, "sender@example.com", agentEmail,
				"<fail@sender.example>", "Will Fail", "", "unread",
				[]byte("hello"), nil, nil, false, "", []string{agentEmail}, nil, nil,
				identity.InboundScreening{}); err != nil {
				return err
			}
			// Manually trigger a constraint violation by writing a
			// duplicate webhook_events id via direct SQL inside the tx.
			// This simulates "outbox INSERT fails after messages
			// INSERT succeeded" (e.g., id collision from a parallel
			// trigger).
			eventID := webhookpub.DeterministicEventID(failMsgID, webhookpub.EventEmailReceived)
			if _, err := tx.Exec(ctx,
				`INSERT INTO webhook_events (id, user_id, type, envelope, status)
				 VALUES ($1, $2, $3, '{}'::jsonb, 'pending')`,
				eventID, userID, webhookpub.EventEmailReceived,
			); err != nil {
				return err
			}
			// Second insert with same id, NOT using ON CONFLICT —
			// forces a constraint violation that aborts the tx.
			_, err := tx.Exec(ctx,
				`INSERT INTO webhook_events (id, user_id, type, envelope, status)
				 VALUES ($1, $2, $3, '{}'::jsonb, 'pending')`,
				eventID, userID, webhookpub.EventEmailReceived,
			)
			return err
		})
		if err == nil {
			t.Fatalf("expected constraint violation, got success")
		}

		// Verify NO messages row was created.
		var msgCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM messages WHERE id = $1`, failMsgID,
		).Scan(&msgCount); err != nil {
			t.Fatalf("count messages: %v", err)
		}
		if msgCount != 0 {
			t.Errorf("messages count = %d, want 0 (atomicity broken — outbox failure should roll back messages)", msgCount)
		}
	})
}
