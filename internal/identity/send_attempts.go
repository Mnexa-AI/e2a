package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// SendAttemptStaleWindow is how long an 'attempting' send_attempts row
// stays "owned" by the original worker before the next caller is
// allowed to take it over. Bounded above by outbound.SMTPRelay's
// worst-case retry envelope (~6.5min) plus headroom — kept tighter
// would risk concurrent SES sends if a real upstream stall happened.
const SendAttemptStaleWindow = 10 * time.Minute

// ErrSendInProgress is returned by ApproveAndSend (and the underlying
// ClaimSendAttempt) when a concurrent attempt for the same message
// is still in-flight at the SES layer. Callers should treat this as
// transient — the in-flight caller will either commit (the next
// retry sees status='sent' and replays) or time out (the row goes
// stale and the next retry takes over).
var ErrSendInProgress = errors.New("send already in progress for this message")

// SendAttemptOutcome describes the result of trying to reserve a
// (message_id) slot for an upstream SES send.
type SendAttemptOutcome int

const (
	// SendAttemptAcquired — caller now owns the slot and must follow
	// up with MarkSendSucceeded or MarkSendFailed.
	SendAttemptAcquired SendAttemptOutcome = iota
	// SendAttemptAlreadySent — a prior attempt for this message
	// already succeeded at SES; SendResult is populated with the
	// recorded provider id and recipient lists. Callers must NOT
	// re-invoke the upstream send.
	SendAttemptAlreadySent
	// SendAttemptInFlight — a concurrent caller holds the slot and
	// the row is not stale. Callers should surface ErrSendInProgress.
	SendAttemptInFlight
)

// SendAttemptResult bundles the outcome with the cached SendResult
// when the outcome is SendAttemptAlreadySent.
type SendAttemptResult struct {
	Outcome SendAttemptOutcome
	Sent    SendResult
}

// ClaimSendAttempt atomically reserves the send_attempts row for
// messageID. Concurrency model mirrors internal/idempotency.Claim:
// one UPSERT with a stale-takeover WHERE clause; the loser does a
// follow-up SELECT to classify the existing row.
//
// Allowed transitions:
//
//	(no row)                              → acquired (fresh INSERT)
//	status='failed'                       → acquired (UPSERT path takes over)
//	status='attempting' AND stale         → acquired (UPSERT path takes over)
//	status='attempting' AND NOT stale     → InFlight (refuse)
//	status='sent'                         → AlreadySent (reuse recorded result)
//
// Called from ApproveAndSend in a SEPARATE small transaction so the
// claim row outlives any rollback of the surrounding approval
// transaction. That's what closes the documented double-send window:
// once status='sent' is committed here, a retry sees AlreadySent and
// skips the upstream send even if the messages-row UPDATE inside
// the approval tx never committed.
func (s *Store) ClaimSendAttempt(ctx context.Context, messageID string) (SendAttemptResult, error) {
	if messageID == "" {
		return SendAttemptResult{}, errors.New("identity: messageID required")
	}

	staleSecs := int(SendAttemptStaleWindow.Seconds())

	var owned int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO send_attempts (
		     message_id, status, attempted_at, completed_at,
		     provider_message_id, method,
		     to_recipients, cc_recipients, bcc_recipients,
		     error
		 )
		 VALUES ($1, 'attempting', now(), NULL, '', '', '{}', '{}', '{}', '')
		 ON CONFLICT (message_id) DO UPDATE
		    SET status              = 'attempting',
		        attempted_at        = now(),
		        completed_at        = NULL,
		        provider_message_id = '',
		        method              = '',
		        to_recipients       = '{}',
		        cc_recipients       = '{}',
		        bcc_recipients      = '{}',
		        error               = ''
		  WHERE send_attempts.status = 'failed'
		     OR (send_attempts.status = 'attempting'
		         AND send_attempts.attempted_at < now() - make_interval(secs => $2))
		 RETURNING 1`,
		messageID, staleSecs,
	).Scan(&owned)
	if err == nil {
		return SendAttemptResult{Outcome: SendAttemptAcquired}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SendAttemptResult{}, err
	}

	// Lost the race — classify the existing row.
	var (
		status   string
		provider string
		method   string
		to       []string
		cc       []string
		bcc      []string
	)
	err = s.pool.QueryRow(ctx,
		`SELECT status, provider_message_id, method,
		        to_recipients, cc_recipients, bcc_recipients
		   FROM send_attempts
		  WHERE message_id = $1`,
		messageID,
	).Scan(&status, &provider, &method, &to, &cc, &bcc)
	if err != nil {
		return SendAttemptResult{}, err
	}

	switch status {
	case "sent":
		return SendAttemptResult{
			Outcome: SendAttemptAlreadySent,
			Sent: SendResult{
				ProviderMessageID: provider,
				Method:            method,
				To:                to,
				CC:                cc,
				BCC:               bcc,
			},
		}, nil
	case "attempting":
		// Not stale (the UPSERT WHERE refused to take over) — another
		// worker holds the slot.
		return SendAttemptResult{Outcome: SendAttemptInFlight}, nil
	default:
		// 'failed' should have been taken over by the UPSERT; if we
		// see it here treat as InFlight defensively rather than
		// silently re-sending.
		return SendAttemptResult{Outcome: SendAttemptInFlight}, nil
	}
}

// MarkSendSucceeded records the result of a successful upstream send.
// Idempotent against double-call: only updates rows still 'attempting'
// so a stray re-Mark from a buggy caller cannot overwrite a previous
// success or revive a failed row.
func (s *Store) MarkSendSucceeded(ctx context.Context, messageID string, r SendResult) error {
	// pgx serializes a nil []string as SQL NULL, which would violate
	// the NOT NULL constraint on cc_recipients / bcc_recipients (the
	// DEFAULT '{}' only fires when the column is omitted from
	// INSERT/UPDATE, not when an explicit NULL is supplied). Coerce
	// here so callers can pass partial SendResults without thinking
	// about it.
	to := r.To
	if to == nil {
		to = []string{}
	}
	cc := r.CC
	if cc == nil {
		cc = []string{}
	}
	bcc := r.BCC
	if bcc == nil {
		bcc = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE send_attempts
		    SET status              = 'sent',
		        completed_at        = now(),
		        provider_message_id = $2,
		        method              = $3,
		        to_recipients       = $4,
		        cc_recipients       = $5,
		        bcc_recipients      = $6,
		        error               = ''
		  WHERE message_id = $1 AND status = 'attempting'`,
		messageID, r.ProviderMessageID, r.Method, to, cc, bcc,
	)
	return err
}

// MarkSendFailed records that the upstream send failed for messageID,
// so the next ClaimSendAttempt is allowed to take over and retry.
// Idempotent against double-call: only updates rows still 'attempting'.
func (s *Store) MarkSendFailed(ctx context.Context, messageID, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE send_attempts
		    SET status       = 'failed',
		        completed_at = now(),
		        error        = $2
		  WHERE message_id = $1 AND status = 'attempting'`,
		messageID, errMsg,
	)
	return err
}
