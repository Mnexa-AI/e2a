package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net"

	"github.com/emersion/go-smtp"
	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// acceptInbound is the queue-first accept-tx (E2A_INBOUND_MODE=async): it durably
// lands the raw MIME in inbound_intake and enqueues one River processing job per
// NEW recipient, all in ONE transaction, BEFORE returning 250. This is the only work
// gating 250 — parse/screen/persist/deliver run later in the worker.
//
// A committed accept ⇒ 250 (responsibility taken; River delivers at-least-once). A
// commit failure ⇒ 451, so the sending MTA retries the whole message instead of us
// losing it. The dedup key (recipient, message_id, content_hash) makes a lost-ack
// retry an idempotent no-op accept — the duplicate re-uses the row and enqueues no
// second job.
func (s *session) acceptInbound(ctx context.Context, body []byte, info threadInfo) error {
	contentHash := contentHashHex(body)
	envelopeFrom := extractEmail(s.from)
	remoteIP := ""
	if s.remoteIP != nil {
		remoteIP = s.remoteIP.String() // stored as text; nil would serialize to "<nil>"
	}
	messageID := info.MessageID // sender's RFC 5322 Message-ID (may be "")

	err := s.relay.store.WithTx(ctx, func(tx pgx.Tx) error {
		for _, rcpt := range s.recipients {
			intakeID := identity.NewInboundIntakeID()
			inserted, e := s.relay.store.InsertInboundIntakeTx(ctx, tx, intakeID, rcpt, envelopeFrom, remoteIP, messageID, contentHash, body)
			if e != nil {
				return e
			}
			if !inserted {
				// Duplicate (lost-ack MTA retry): the row + its job already exist —
				// enqueue nothing, still answer 250. Idempotent accept.
				log.Printf("[%s] inbound dedup hit for %s (msgid=%q) — no re-enqueue", s.id, rcpt, messageID)
				continue
			}
			jobID, e := s.relay.inboundEnq.EnqueueInboundProcessTx(ctx, tx, intakeID)
			if e != nil {
				return e
			}
			if e := s.relay.store.StampInboundIntakeJobIDTx(ctx, tx, intakeID, jobID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		// Nothing committed (one tx, all-or-nothing) — 451 so the sender retries.
		log.Printf("[%s] inbound accept-tx failed → 451 (sender will retry): %v", s.id, err)
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "temporary failure accepting message; please retry"}
	}
	log.Printf("[%s] inbound accepted (async) recipients=%d msgid=%q", s.id, len(s.recipients), messageID)
	return nil
}

// contentHashHex is the dedup content fingerprint — sha256 of the raw MIME, hex.
func contentHashHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ProcessIntake runs the full inbound chain for an accepted inbound_intake row — the
// async River worker's entry point (internal/inboundprocess.Processor). It rebuilds
// the connection context from the persisted row (the worker has no live session) and
// calls the shared processInbound with a hook that flips the intake to 'processed'
// ATOMICALLY with the messages insert + event publish — the worker's idempotency
// gate. The stored remote_ip text is parsed back to net.IP for SPF; an unparseable
// value yields nil, which emailauth.Check treats as an unauthenticated source
// (fail-safe, not a crash).
func (srv *Server) ProcessIntake(ctx context.Context, it *identity.InboundIntake) error {
	in := inboundInput{
		Body:         it.Raw,
		EnvelopeFrom: it.EnvelopeFrom,
		RemoteIP:     net.ParseIP(it.RemoteIP),
		Recipient:    it.Recipient,
		TraceID:      it.ID,
	}
	hook := func(ctx context.Context, tx pgx.Tx, messageID string) error {
		return srv.store.MarkInboundIntakeProcessedTx(ctx, tx, it.ID, messageID)
	}
	return srv.processInbound(ctx, in, hook)
}
