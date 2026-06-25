// Package hitlnotify sends the approval notification email that fires
// whenever a new outbound message enters pending_review.
//
// The notification is the reviewer's primary touchpoint with HITL — it
// arrives in the account owner's inbox with a preview of the held
// message and one-click approve / reject magic links, plus a link back
// to the dashboard for edit-before-approve.
//
// The notifier is intentionally best-effort: delivery failures are
// logged but never surfaced as HTTP errors, so a broken relay cannot
// block the send-hold contract the API promises to its SDK/CLI users.
package hitlnotify

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// notifyLocalPart is the fixed local-part of the notification sender
// address. Reusing a single from-address lets mail clients group all HITL
// notifications into a single conversation / filter.
const notifyLocalPart = "hitl-noreply"

// tokenGraceAfterTTL extends the magic-link token's exp slightly past the
// message's approval_expires_at so a click received just before TTL is
// still honored — the expiration worker is the authoritative TTL gate.
const tokenGraceAfterTTL = 10 * time.Minute

// Notifier sends approval notification emails. Construct with New, then
// call NotifyPendingApproval from the HITL gate right after the pending
// row is written. Errors are logged, never returned upstream.
type Notifier struct {
	store      *identity.Store
	relay      *outbound.SMTPRelay
	signer     *approvaltoken.Signer
	fromDomain string
	publicURL  string
}

// New returns a Notifier that sends mail through relay using the given
// public URL to build magic-link URLs. fromDomain is the platform
// relay's from-domain — e.g. "send.example.com" — which is combined with
// the fixed local-part to produce the From address.
func New(store *identity.Store, relay *outbound.SMTPRelay, signer *approvaltoken.Signer, fromDomain, publicURL string) *Notifier {
	return &Notifier{
		store:      store,
		relay:      relay,
		signer:     signer,
		fromDomain: fromDomain,
		publicURL:  strings.TrimRight(publicURL, "/"),
	}
}

// NotifyPendingApproval composes and sends the notification email for a
// newly held message. Designed to be called in a goroutine from the HTTP
// handler — any returned error is only for tests; production callers
// should ignore it and rely on the notifier's own logging.
func (n *Notifier) NotifyPendingApproval(ctx context.Context, msg *identity.Message, agent *identity.AgentIdentity) error {
	if n == nil {
		return nil
	}
	if msg == nil || agent == nil {
		return fmt.Errorf("notify: msg or agent is nil")
	}
	if msg.ApprovalExpiresAt == nil {
		return fmt.Errorf("notify: approval_expires_at is nil on msg %s", msg.ID)
	}

	owner, err := n.store.GetUserByID(ctx, agent.UserID)
	if err != nil {
		return fmt.Errorf("notify: lookup owner: %w", err)
	}
	if owner.Email == "" {
		return fmt.Errorf("notify: owner %s has no email on record", owner.ID)
	}

	tokenExp := msg.ApprovalExpiresAt.Add(tokenGraceAfterTTL)

	// Magic-link tokens are signed with the deployment HMAC secret
	// (cfg.Signing.HMACSecret) via n.signer — the sole signer.
	signFn := func(action string) (string, error) {
		return n.signer.Sign(msg.ID, action, tokenExp)
	}

	approveTok, err := signFn(approvaltoken.ActionApprove)
	if err != nil {
		return fmt.Errorf("notify: sign approve token: %w", err)
	}
	rejectTok, err := signFn(approvaltoken.ActionReject)
	if err != nil {
		return fmt.Errorf("notify: sign reject token: %w", err)
	}

	subject := fmt.Sprintf("[e2a] approve outbound from %s: %s",
		agent.EmailAddress(), truncate(msg.Subject, 60))

	approveURL := n.magicURL("/v1/approve", approveTok)
	rejectURL := n.magicURL("/v1/reject", rejectTok)
	dashboardURL := n.dashboardURL(msg.ID)

	text := renderText(msg, agent, approveURL, rejectURL, dashboardURL)
	htmlBody := renderHTML(msg, agent, approveURL, rejectURL, dashboardURL)

	fromAddr := fmt.Sprintf("%s@%s", notifyLocalPart, n.fromDomain)
	fromHeader := fmt.Sprintf("e2a <%s>", fromAddr)

	message, err := outbound.ComposeMultipartMessage(
		fromHeader, []string{owner.Email}, nil,
		subject, text, htmlBody,
		"",            // no reply-to-message-id (fresh notification)
		nil,           // no references chain (fresh notification)
		n.fromDomain,  // from_domain (Message-ID generation)
		fromAddr,      // reply_to — point replies back at the platform, not the agent
		"",            // no conversation_id
	)
	if err != nil {
		return fmt.Errorf("notify: compose: %w", err)
	}

	if _, err := n.relay.Send(fromAddr, []string{owner.Email}, message); err != nil {
		return fmt.Errorf("notify: smtp send: %w", err)
	}

	log.Printf("[hitl-notify] sent approval email: msg=%s owner=%s agent=%s",
		msg.ID, owner.Email, agent.ID)
	return nil
}

// NotifyPendingApprovalAsync is a thin fire-and-forget wrapper suitable
// for goroutine launches from HTTP handlers. It swallows the error
// after logging so callers don't need to.
func (n *Notifier) NotifyPendingApprovalAsync(msg *identity.Message, agent *identity.AgentIdentity) {
	if n == nil {
		// Operator-side misconfiguration: notifier wasn't wired (most
		// likely because OutboundSMTP.FromDomain or HTTP.PublicURL is
		// unset; the wiring in cmd/e2a/main.go gates on both). The API
		// still returns 202 pending_review to the caller, but the
		// reviewer never gets an email — silent and confusing without
		// a log line.
		msgID := ""
		if msg != nil {
			msgID = msg.ID
		}
		log.Printf("[hitl-notify] suppressed (notifier not configured): msg=%s", msgID)
		return
	}
	go func() {
		// Detach from the request context so shutting down mid-notification
		// doesn't drop the email. Cap the total send time generously.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := n.NotifyPendingApproval(ctx, msg, agent); err != nil {
			log.Printf("[hitl-notify] send failed: msg=%s err=%v", msg.ID, err)
		}
	}()
}

func (n *Notifier) magicURL(path, token string) string {
	if n.publicURL == "" {
		return path + "?t=" + url.QueryEscape(token)
	}
	return n.publicURL + path + "?t=" + url.QueryEscape(token)
}

func (n *Notifier) dashboardURL(messageID string) string {
	if n.publicURL == "" {
		return "/dashboard/pending/" + messageID
	}
	return n.publicURL + "/dashboard/pending/" + messageID
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// The notification deliberately omits the held message's body from the
// email. The body lives in the database and is shown on the token-gated
// confirmation page the magic link leads to; keeping it out of the
// email avoids leaking sensitive draft content through the reviewer's
// mail infrastructure (spam filters, corporate archives, mobile sync,
// etc.). Reviewers see recipients and subject here — enough to know
// which message is waiting — and the full body only after they click.

func renderText(msg *identity.Message, agent *identity.AgentIdentity, approveURL, rejectURL, dashboardURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Your agent %s wants to send a message.\n\n", agent.EmailAddress())
	if len(msg.ToRecipients) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(msg.ToRecipients, ", "))
	}
	if len(msg.CC) > 0 {
		fmt.Fprintf(&b, "Cc: %s\n", strings.Join(msg.CC, ", "))
	}
	if len(msg.BCC) > 0 {
		fmt.Fprintf(&b, "Bcc: %s\n", strings.Join(msg.BCC, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\n", msg.Subject)
	if msg.ApprovalExpiresAt != nil {
		fmt.Fprintf(&b, "Expires: %s\n", msg.ApprovalExpiresAt.UTC().Format(time.RFC1123))
	}
	b.WriteString("\nThe full body is not included in this email. Open a link\n")
	b.WriteString("below to review the message before approving or rejecting.\n\n")

	fmt.Fprintf(&b, "Review and approve:\n  %s\n\n", approveURL)
	fmt.Fprintf(&b, "Review and reject:\n  %s\n\n", rejectURL)
	fmt.Fprintf(&b, "Edit before approving (dashboard):\n  %s\n\n", dashboardURL)
	fmt.Fprintf(&b, "If no action is taken by the expiration time above, the\n")
	fmt.Fprintf(&b, "message will be finalized according to the agent's\n")
	fmt.Fprintf(&b, "configured auto-expiration policy.\n")
	return b.String()
}

// renderHTML builds the approval email in the dashboard's "Loft" palette so the
// notification reads as the same product as the web app: a warm cream shell, a
// white card, the Geist UI type stack, ember links, and the web app's semantic
// success/danger button shades. Colors are hardcoded (no CSS vars or @media) and
// the layout is table-based so it survives mail clients. Token values mirror
// web/src/app/globals.css; keep them in sync if the brand palette moves.
func renderHTML(msg *identity.Message, agent *identity.AgentIdentity, approveURL, rejectURL, dashboardURL string) string {
	const (
		fontStack = `"Geist",-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif`
		bg        = "#FAF7F2" // --bg (cream shell)
		panel     = "#FFFFFF" // --bg-panel (card)
		border    = "#E5DED3" // --border
		fg        = "#1A1714" // --fg-strong
		muted     = "#6E665B" // --fg-muted
		subtle    = "#9A9082" // --fg-subtle
		link      = "#A84218" // --accent-strong (ember)
		success   = "#0F7A4D" // --success (approve)
		danger    = "#CC2E2E" // --danger (reject)
		onAccent  = "#FFFFFF" // --accent-fg
	)
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html><body style="margin:0;padding:24px 16px;background:%s;font-family:%s;color:%s;line-height:1.5">`, bg, fontStack, fg)
	fmt.Fprintf(&b, `<div style="max-width:560px;margin:0 auto;background:%s;border:1px solid %s;border-radius:10px;padding:28px">`, panel, border)

	fmt.Fprintf(&b, `<p style="margin:0 0 4px;font-size:15px">Your agent <strong>%s</strong> wants to send a message.</p>`,
		html.EscapeString(agent.EmailAddress()))

	fmt.Fprintf(&b, `<table style="font-size:14px;color:%s;border-collapse:collapse;margin:16px 0" cellpadding="4">`, fg)
	if len(msg.ToRecipients) > 0 {
		fmt.Fprintf(&b, `<tr><td style="color:%s">To</td><td>%s</td></tr>`,
			subtle, html.EscapeString(strings.Join(msg.ToRecipients, ", ")))
	}
	if len(msg.CC) > 0 {
		fmt.Fprintf(&b, `<tr><td style="color:%s">Cc</td><td>%s</td></tr>`,
			subtle, html.EscapeString(strings.Join(msg.CC, ", ")))
	}
	if len(msg.BCC) > 0 {
		fmt.Fprintf(&b, `<tr><td style="color:%s">Bcc</td><td>%s</td></tr>`,
			subtle, html.EscapeString(strings.Join(msg.BCC, ", ")))
	}
	fmt.Fprintf(&b, `<tr><td style="color:%s">Subject</td><td><strong>%s</strong></td></tr>`,
		subtle, html.EscapeString(msg.Subject))
	if msg.ApprovalExpiresAt != nil {
		fmt.Fprintf(&b, `<tr><td style="color:%s">Expires</td><td>%s</td></tr>`,
			subtle, html.EscapeString(msg.ApprovalExpiresAt.UTC().Format(time.RFC1123)))
	}
	b.WriteString(`</table>`)

	fmt.Fprintf(&b, `<p style="font-size:13px;color:%s">The message body is not included in this email. Click a button below to review it before deciding.</p>`, muted)

	// Action buttons point at the token-gated confirm pages (GET). The
	// actual approve/reject side effect only fires when the reviewer
	// submits the form on that page — this is what keeps mail-client URL
	// scanners from approving on the reviewer's behalf.
	fmt.Fprintf(&b,
		`<p style="margin-top:16px"><a href="%s" style="background:%s;color:%s;font-weight:500;padding:10px 18px;text-decoration:none;border-radius:6px;margin-right:12px">Review &amp; approve</a>`+
			`<a href="%s" style="background:%s;color:%s;font-weight:500;padding:10px 18px;text-decoration:none;border-radius:6px">Review &amp; reject</a></p>`,
		html.EscapeString(approveURL), success, onAccent,
		html.EscapeString(rejectURL), danger, onAccent)

	fmt.Fprintf(&b,
		`<p style="margin-top:16px;font-size:13px;color:%s">Need to edit before approving? <a href="%s" style="color:%s">Review in the dashboard</a>.</p>`,
		muted, html.EscapeString(dashboardURL), link)

	fmt.Fprintf(&b, `<p style="margin-top:24px;font-size:12px;color:%s">If no action is taken before the expiration time, the message will be finalized according to the agent's configured auto-expiration policy.</p>`, subtle)
	b.WriteString(`</div></body></html>`)
	return b.String()
}
