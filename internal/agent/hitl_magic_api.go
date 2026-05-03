package agent

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// The magic-link flow is split into GET (render a confirmation page) and
// POST (execute the action). This keeps email-client URL scanners —
// Gmail, Outlook Safe Links, Slack unfurls, corporate mail gateways —
// from accidentally approving or rejecting on behalf of the reviewer
// when they preview the link. The reviewer has to actually load the
// page in a browser and click the confirm button.
//
// GET reads the token from ?t=; POST reads it from a hidden form field.
// The token is never transmitted in a way that lets a passive URL
// follower trigger the state change.

// --- GET confirmation pages ---

func (a *API) handleApproveMagicLinkGet(w http.ResponseWriter, r *http.Request) {
	a.renderConfirmPage(w, r, approvaltoken.ActionApprove)
}

func (a *API) handleRejectMagicLinkGet(w http.ResponseWriter, r *http.Request) {
	a.renderConfirmPage(w, r, approvaltoken.ActionReject)
}

// renderConfirmPage validates the token, loads the pending message, and
// renders an HTML page with a POST form targeting the same endpoint.
// The body preview lives on this page — not in the notification email —
// so sensitive content stays behind a token-gated server render rather
// than passing through the reviewer's mail infrastructure.
func (a *API) renderConfirmPage(w http.ResponseWriter, r *http.Request, endpointAction string) {
	if a.approvalSigner == nil {
		http.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("t")
	claims, status, errMsg := a.verifyMagicToken(token, endpointAction)
	if errMsg != "" {
		writeMagicMessage(w, status, pageTitleForAction(endpointAction, "Invalid link"), errMsg)
		return
	}

	// Load message via ownership-scoped read so the confirmation page
	// surfaces the real detail (recipients, subject, body preview).
	userID, _, err := a.store.ResolveOutboundOwner(r.Context(), claims.MessageID)
	if err != nil {
		writeMagicMessage(w, http.StatusNotFound, "Message not found",
			"This message no longer exists.")
		return
	}
	msg, err := a.store.GetOutboundMessageForUser(r.Context(), claims.MessageID, userID)
	if err != nil {
		writeMagicMessage(w, http.StatusNotFound, "Message not found",
			"This message no longer exists.")
		return
	}
	if msg.Status != identity.MessageStatusPendingApproval {
		writeMagicMessage(w, http.StatusConflict, "Already resolved",
			"This message has already been approved, rejected, or expired.")
		return
	}

	writeConfirmPage(w, http.StatusOK, endpointAction, token, msg)
}

// --- POST executors ---

func (a *API) handleApproveMagicLinkPost(w http.ResponseWriter, r *http.Request) {
	if a.approvalSigner == nil {
		http.NotFound(w, r)
		return
	}
	token := extractFormToken(r)
	claims, status, errMsg := a.verifyMagicToken(token, approvaltoken.ActionApprove)
	if errMsg != "" {
		writeMagicMessage(w, status, "Invalid confirmation", errMsg)
		return
	}
	userID, agentID, err := a.store.ResolveOutboundOwner(r.Context(), claims.MessageID)
	if err != nil {
		writeMagicMessage(w, http.StatusNotFound, "Message not found",
			"This message no longer exists.")
		return
	}
	a.magicApprove(w, r, claims.MessageID, userID, agentID)
}

func (a *API) handleRejectMagicLinkPost(w http.ResponseWriter, r *http.Request) {
	if a.approvalSigner == nil {
		http.NotFound(w, r)
		return
	}
	token := extractFormToken(r)
	claims, status, errMsg := a.verifyMagicToken(token, approvaltoken.ActionReject)
	if errMsg != "" {
		writeMagicMessage(w, status, "Invalid confirmation", errMsg)
		return
	}
	userID, _, err := a.store.ResolveOutboundOwner(r.Context(), claims.MessageID)
	if err != nil {
		writeMagicMessage(w, http.StatusNotFound, "Message not found",
			"This message no longer exists.")
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		reason = "magic-link rejection"
	}
	a.magicReject(w, r, claims.MessageID, userID, reason)
}

// extractFormToken pulls the token from standard form encoding. Request
// body is expected to be application/x-www-form-urlencoded since the
// confirmation page submits a plain HTML form.
func extractFormToken(r *http.Request) string {
	_ = r.ParseForm()
	return r.FormValue("t")
}

// verifyMagicToken runs the HMAC + exp + action whitelist checks and
// returns either claims or an (HTTP status, user-visible message) pair.
// Shared by GET and POST so both paths reject the same inputs the same
// way.
//
// HMAC verification first tries the *owning user's* per-account signing
// secrets — multiple may be valid during a rotation window. If those
// fail (or we can't resolve the user), fall back to the deployment-wide
// signer for legacy tokens issued before per-user secrets shipped.
func (a *API) verifyMagicToken(token, endpointAction string) (*approvaltoken.Claims, int, string) {
	if token == "" {
		return nil, http.StatusBadRequest,
			"This approval link is missing its token."
	}

	claims, err := a.verifyTokenAnySecret(context.Background(), token)
	if err != nil {
		if errors.Is(err, approvaltoken.ErrTokenExpired) {
			return nil, http.StatusGone,
				"This approval link has expired. Visit the dashboard to review pending messages."
		}
		return nil, http.StatusBadRequest, "This approval link isn't valid."
	}
	if claims.Action != endpointAction {
		return nil, http.StatusBadRequest,
			"This approval link isn't valid for this action."
	}
	return claims, 0, ""
}

// verifyTokenAnySecret tries the owning user's per-account secrets
// first, then falls back to the deployment-wide signer. The per-user
// path is the primary route post-migration; the fallback exists for
// (a) tokens issued before the migration ran and (b) self-host
// configurations where the deployment secret is the only one set.
//
// The pre-parse of the token's message_id is unverified — its only
// use is to look up which user's secrets to try. A forged message_id
// causes us to load the wrong user's secrets, which won't verify the
// forged signature, so the token is correctly rejected.
func (a *API) verifyTokenAnySecret(ctx context.Context, token string) (*approvaltoken.Claims, error) {
	if messageID, err := approvaltoken.PeekMessageID(token); err == nil && messageID != "" {
		userID, _, ownerErr := a.store.ResolveOutboundOwner(ctx, messageID)
		if ownerErr == nil && userID != "" {
			if userSecrets, secretsErr := a.store.GetUserSigningSecrets(ctx, userID); secretsErr == nil && len(userSecrets) > 0 {
				values := make([]string, len(userSecrets))
				for i, s := range userSecrets {
					values[i] = s.Secret
				}
				if claims, err := approvaltoken.Verify(values, token); err == nil {
					return claims, nil
				} else if errors.Is(err, approvaltoken.ErrTokenExpired) {
					// Don't attempt deployment-secret fallback if the token
					// already verified against a user secret but is just
					// past its TTL — that's an expired-token signal, not
					// an invalid-secret one.
					return nil, err
				}
			}
		}
	}
	if a.approvalSigner != nil {
		return a.approvalSigner.Verify(token)
	}
	return nil, approvaltoken.ErrInvalidToken
}

// --- Action implementations (called after POST + token verify) ---

func (a *API) magicApprove(w http.ResponseWriter, r *http.Request, messageID, userID, agentID string) {
	agent, err := a.store.GetAgentByID(r.Context(), agentID)
	if err != nil {
		log.Printf("[api] magic-approve: agent lookup failed: msg=%s agent=%s err=%v", messageID, agentID, err)
		writeMagicMessage(w, http.StatusInternalServerError,
			"Something went wrong",
			"We couldn't find the agent for this message. Try the dashboard.")
		return
	}
	if !agent.DomainVerified {
		writeMagicMessage(w, http.StatusForbidden,
			"Agent not verified",
			"Cannot approve: the agent's domain is no longer verified.")
		return
	}

	sent, err := a.store.ApproveAndSend(r.Context(), messageID, userID, identity.PendingApprovalEdit{},
		func(locked *identity.Message) (identity.SendResult, error) {
			sendReq, err := buildSendRequestFromMessage(locked)
			if err != nil {
				return identity.SendResult{}, err
			}
			attachReferencesChain(r.Context(), a.store, agent.ID, &sendReq)
			result, err := a.sender.Send(agent, sendReq)
			if err != nil {
				return identity.SendResult{}, err
			}
			return identity.SendResult{
				ProviderMessageID: result.MessageID,
				Method:            result.Method,
				To:                result.To,
				CC:                result.CC,
				BCC:               result.BCC,
			}, nil
		})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			writeMagicMessage(w, http.StatusNotFound,
				"Message not found",
				"This message no longer exists.")
		case errors.Is(err, identity.ErrNotPendingApproval):
			writeMagicMessage(w, http.StatusConflict,
				"Already resolved",
				"This message has already been approved, rejected, or expired.")
		default:
			var ve *outbound.ValidationError
			if errors.As(err, &ve) {
				writeMagicMessage(w, http.StatusBadRequest, "Cannot send",
					html.EscapeString(ve.Error()))
				return
			}
			log.Printf("[api] magic-approve send failed: msg=%s err=%v", messageID, err)
			writeMagicMessage(w, http.StatusInternalServerError,
				"Send failed",
				"The message couldn't be sent. Try again from the dashboard.")
		}
		return
	}

	if _, err := a.usage.RecordAndCheck(r.Context(), userID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] magic-approve usage error: %v", err)
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=sent agent=%s to=%v approved=magic-link:user:%s",
		sent.ID, sent.Type, agent.EmailAddress(), sent.ToRecipients, userID)

	writeMagicMessage(w, http.StatusOK,
		"Approved",
		fmt.Sprintf("Your message to %s has been sent.", html.EscapeString(firstRecipient(sent.ToRecipients))))
}

func (a *API) magicReject(w http.ResponseWriter, r *http.Request, messageID, userID, reason string) {
	rejected, err := a.store.RejectPending(r.Context(), messageID, userID, reason)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			writeMagicMessage(w, http.StatusNotFound, "Message not found",
				"This message no longer exists.")
		case errors.Is(err, identity.ErrNotPendingApproval):
			writeMagicMessage(w, http.StatusConflict, "Already resolved",
				"This message has already been approved, rejected, or expired.")
		default:
			log.Printf("[api] magic-reject failed: msg=%s err=%v", messageID, err)
			writeMagicMessage(w, http.StatusInternalServerError, "Rejection failed",
				"We couldn't reject this message. Try again from the dashboard.")
		}
		return
	}
	log.Printf("[mail:%s] dir=outbound type=%s status=rejected rejected_by=magic-link:user:%s reason=%q",
		rejected.ID, rejected.Type, userID, reason)

	writeMagicMessage(w, http.StatusOK, "Rejected",
		"The message has been discarded and will not be sent.")
}

// --- HTML rendering ---

func pageTitleForAction(action, fallback string) string {
	switch action {
	case approvaltoken.ActionApprove:
		return "Approve message"
	case approvaltoken.ActionReject:
		return "Reject message"
	default:
		return fallback
	}
}

// writeMagicMessage renders a bare confirmation / error page.
func writeMagicMessage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Block embedding so any approved/rejected success page can't be
	// iframed into a page that pretends to be legitimate.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// Discourage indexing if a link ever leaks publicly.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>%s — e2a</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  max-width: 520px; margin: 80px auto; padding: 0 20px; color: #111; line-height: 1.5; }
h1 { font-size: 22px; margin-bottom: 12px; }
p { color: #555; }
</style>
</head>
<body>
<h1>%s</h1>
<p>%s</p>
</body>
</html>
`, html.EscapeString(title), html.EscapeString(title), body)
}

// writeConfirmPage renders the token-gated preview + POST form. This is
// where the body preview lives — the reviewer has already opened the
// link in their browser, so showing held content here is acceptable.
// The form targets the same URL path with method POST so the actual
// state change only fires on an explicit click.
func writeConfirmPage(w http.ResponseWriter, status int, action, token string, msg *identity.Message) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)

	var actionPath, submitLabel, submitStyle, title, lede string
	switch action {
	case approvaltoken.ActionApprove:
		actionPath = "/api/v1/approve"
		submitLabel = "Approve & send"
		submitStyle = "background:#0a7b3f;color:#fff"
		title = "Approve message"
		lede = "This message will be sent from your agent immediately."
	case approvaltoken.ActionReject:
		actionPath = "/api/v1/reject"
		submitLabel = "Reject"
		submitStyle = "background:#b53030;color:#fff"
		title = "Reject message"
		lede = "This message will be discarded and not sent."
	}

	bodyPreview := msg.BodyText
	if bodyPreview == "" && msg.BodyHTML != "" {
		bodyPreview = "(HTML only; view full message in the dashboard)"
	}

	toList := strings.Join(msg.ToRecipients, ", ")
	ccList := strings.Join(msg.CC, ", ")
	bccList := strings.Join(msg.BCC, ", ")

	var rows strings.Builder
	fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">Agent</td><td><code>%s</code></td></tr>`,
		html.EscapeString(msg.AgentID))
	if toList != "" {
		fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">To</td><td>%s</td></tr>`,
			html.EscapeString(toList))
	}
	if ccList != "" {
		fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">Cc</td><td>%s</td></tr>`,
			html.EscapeString(ccList))
	}
	if bccList != "" {
		fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">Bcc</td><td>%s</td></tr>`,
			html.EscapeString(bccList))
	}
	fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">Subject</td><td><strong>%s</strong></td></tr>`,
		html.EscapeString(msg.Subject))
	if msg.ApprovalExpiresAt != nil {
		fmt.Fprintf(&rows, `<tr><td style="color:#888;padding:2px 8px 2px 0">Expires</td><td>%s</td></tr>`,
			html.EscapeString(msg.ApprovalExpiresAt.UTC().Format(time.RFC1123)))
	}

	// Optional rejection-reason input only on /reject.
	reasonField := ""
	if action == approvaltoken.ActionReject {
		reasonField = `<p style="margin-top:14px">
<label for="reason" style="display:block;font-size:13px;color:#888;margin-bottom:4px">Reason (optional)</label>
<input id="reason" name="reason" type="text" maxlength="200" placeholder="e.g. wrong tone, bad recipient"
  style="width:100%;padding:8px 10px;border:1px solid #ccc;border-radius:6px;font-family:inherit;font-size:14px">
</p>`
	}

	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>%s — e2a</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  max-width: 560px; margin: 48px auto; padding: 0 20px; color: #111; line-height: 1.5; }
h1 { font-size: 22px; margin: 0 0 8px; }
.lede { color: #666; margin: 0 0 20px; }
table { font-size: 14px; color: #222; margin-bottom: 16px; }
.preview { background:#f6f6f6; border:1px solid #e0e0e0; padding:12px 14px;
  border-radius:6px; white-space:pre-wrap;
  font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:13px;
  max-height: 240px; overflow:auto; }
.submit { border:0; padding:10px 18px; border-radius:6px; font-size:14px;
  font-weight:600; cursor:pointer; }
.cancel { margin-left:12px; color:#888; text-decoration:none; font-size:13px; }
.footnote { margin-top:28px; font-size:12px; color:#888; }
</style>
</head>
<body>
<h1>%s</h1>
<p class="lede">%s</p>
<table cellspacing="0">%s</table>
<div class="preview">%s</div>
<form method="POST" action="%s">
<input type="hidden" name="t" value="%s">
%s
<p style="margin-top:16px">
<button type="submit" class="submit" style="%s">%s</button>
<a href="about:blank" class="cancel">Close without acting</a>
</p>
</form>
<p class="footnote">Clicking this button sends the action to e2a. If you close the window without clicking, nothing changes and the message stays pending.</p>
</body>
</html>
`,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(lede),
		rows.String(),
		html.EscapeString(bodyPreview),
		html.EscapeString(actionPath),
		html.EscapeString(token),
		reasonField,
		submitStyle,
		html.EscapeString(submitLabel),
	)
}

func firstRecipient(rs []string) string {
	if len(rs) == 0 {
		return "the recipient"
	}
	return rs[0]
}
