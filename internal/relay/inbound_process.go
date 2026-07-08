package relay

import (
	"context"
	"net"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

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
