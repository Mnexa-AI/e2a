package httpapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/danielgtaylor/huma/v2"
)

// jsonResponse builds an extra OpenAPI response entry for an operation whose
// handler can return a non-default status with the given body schema. The
// schema is registered (and reused via $ref) in the API's component registry,
// so the declared response stays in lockstep with the Go type — no hand-edits
// to api/openapi.yaml. Used to document the 202 (HITL hold) / 412 / 409 codes
// the typed handlers emit but Huma can't infer from the single DefaultStatus.
func (s *Server) jsonResponse(bodyType reflect.Type, schemaName, description string) *huma.Response {
	schema := s.API.OpenAPI().Components.Schemas.Schema(bodyType, true, schemaName)
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: schema},
		},
	}
}

// errorEnvelopeResponse is the generic `default` error response (ErrorEnvelope).
// Huma auto-adds it to every operation, but declaring a custom `Responses` map
// SUPPRESSES that auto default — so any op with a custom map must re-add this, or
// its OpenAPI contract omits the error shape and generated clients fall back to
// raw-string error bodies (losing the machine `code`; api-v1-redesign §6a #4).
func (s *Server) errorEnvelopeResponse() *huma.Response {
	return s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
		"Error — the standard envelope; branch on error.code.")
}

// SendResultView is the single outbound result for send/reply/forward/approve/
// test (MSG-9). Per scenario:
//   - sent:  status="sent" + message_id (the e2a msg_ id) + provider_message_id
//     (SES id) + sent_as + method.
//   - held:  status="pending_review" + message_id + approval_expires_at.
//   - approved+sent: the "sent" set + edited (reviewer edited the draft).
//
// message_id is always the e2a message id (GET-able), never the provider id —
// the SES id is provider_message_id. `reject` keeps its own RejectResultView
// (it is not a send).
type SendResultView struct {
	// review_approved is the inbound-release outcome of POST .../approve (an
	// inbound hold released to the agent's inbox — no send). sent/pending_review
	// are the send/outbound-approve outcomes.
	Status            string     `json:"status" doc:"Outcome. Open set; tolerate unknown values. Known values: accepted, sent, pending_review, review_approved, failed. accepted = durably persisted and queued for submission (async pipeline); the terminal outcome arrives via webhook events (email.sent / email.failed) or GET /v1/messages/{id}. failed = terminal failure. Always branch on this field, not the HTTP status code."`
	MessageID         string     `json:"message_id"`
	ProviderMessageID string     `json:"provider_message_id,omitempty" doc:"Upstream provider (SES) id. Optional/absent until the message is actually sent — an accepted-but-not-yet-sent message has no provider id."`
	SentAs            string     `json:"sent_as,omitempty" doc:"From identity used. Open set; tolerate unknown values. Known values: own_address, relay."`
	Method            string     `json:"method,omitempty" doc:"Send transport. Open set; tolerate unknown values. Known values: smtp, loopback."`
	ApprovalExpiresAt *time.Time `json:"approval_expires_at,omitempty"`
	// Edited is set only by approve (true/false = did the reviewer edit the
	// draft before sending); omitted on the plain send path.
	Edited *bool `json:"edited,omitempty"`
}

// maxOutboundBytes caps the outbound request body (send/reply/forward). It
// matches the legacy 25 MB limit so attachments keep working — Huma's default
// is only 1 MiB, which would silently reject anything but tiny mail.
const maxOutboundBytes = 25 * 1024 * 1024

// maxRecipients caps the combined to+cc+bcc fan-out of a single outbound
// message. A body-size ceiling alone doesn't bound recipient count, so a tiny
// body could still address thousands of addresses; this keeps a single send
// from becoming a blast. Over the cap is a 400 too_many_recipients.
const maxRecipients = 50

// recipientCountError returns a too_many_recipients envelope when the combined
// to+cc+bcc count exceeds maxRecipients, else nil.
func recipientCountError(groups ...[]string) *ErrorEnvelope {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if total > maxRecipients {
		return NewError(http.StatusBadRequest, "too_many_recipients",
			"too many recipients — at most 50 across to, cc and bcc combined").
			WithDetails(map[string]any{"max_recipients": maxRecipients, "provided": total})
	}
	return nil
}

// SendEmailRequest is the new-thread send body. `to` is required (RFC 5321
// requires ≥1 recipient; From/Date are server-set). Content comes in one of
// two mutually exclusive shapes: literal subject + body (+ optional
// html_body) — both required at the handler for a usable new email (MSG-3) —
// or a template reference (template_id XOR template_alias, + template_data),
// which the server renders into subject/body/html_body before any further
// processing. subject/body moved from schema-required to handler-enforced so
// the template shape can omit them.
type SendEmailRequest struct {
	To             []string              `json:"to" nullable:"false"`
	CC             []string              `json:"cc,omitempty" nullable:"false"`
	BCC            []string              `json:"bcc,omitempty" nullable:"false"`
	Subject        string                `json:"subject,omitempty" doc:"Literal subject. Required unless a template reference is used (mutually exclusive with template_id/template_alias)."`
	Body           string                `json:"body,omitempty" doc:"Literal plain-text body. Required unless a template reference is used (mutually exclusive with template_id/template_alias)."`
	HTMLBody       string                `json:"html_body,omitempty" doc:"Literal HTML body. Mutually exclusive with template_id/template_alias."`
	TemplateID     string                `json:"template_id,omitempty" doc:"Send using a stored template (rendered server-side, before any review hold). Mutually exclusive with template_alias and with literal subject/body/html_body. Beta: templates are unstable — their shape may change before they are declared stable."`
	TemplateAlias  string                `json:"template_alias,omitempty" doc:"Send using a stored template resolved by its per-user alias. Mutually exclusive with template_id and with literal subject/body/html_body. Beta: templates are unstable — their shape may change before they are declared stable."`
	TemplateData   TemplateData          `json:"template_data,omitempty" doc:"Variables for the referenced template ({{name}}, dot paths into nested objects). Missing variables render as empty strings. Beta: templates are unstable — their shape may change before they are declared stable."`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false"`
}

type createMessageInput struct {
	Address        string `path:"email"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Wait           string `query:"wait" doc:"Sync-compat valve. wait=sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status=accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome."`
	Body           SendEmailRequest
}

type sendOutput struct {
	Status int
	Body   SendResultView
}

func (s *Server) registerOutbound() {
	// 202 Accepted is the HITL-hold outcome of every outbound op: the message
	// is queued as a pending_review draft rather than sent. Declared
	// explicitly because Huma infers only the single DefaultStatus (200).
	held202 := func() *huma.Response {
		return s.jsonResponse(reflect.TypeOf(SendResultView{}), "SendResultView",
			"Accepted — held for human approval (status=pending_review).")
	}

	huma.Register(s.API, huma.Operation{
		OperationID: "sendMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages",
		Summary: "Send a new email", Tags: []string{"messages"},
		Description:  "Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key.",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": held202(), "default": s.errorEnvelopeResponse()},
	}, s.handleCreateMessage)

	huma.Register(s.API, huma.Operation{
		OperationID: "replyToMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/reply",
		Summary: "Reply to a message", Tags: []string{"messages"},
		Description:  "Reply to a message (inbound or outbound); recipients and threading are derived from the original. Replying to a message the agent received targets its sender; replying to a message the agent sent continues the thread to its original recipients (`reply_all` also re-includes the original Cc). 202 when held for HITL.",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": held202(), "default": s.errorEnvelopeResponse()},
	}, s.handleReply)

	huma.Register(s.API, huma.Operation{
		OperationID: "forwardMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/forward",
		Summary: "Forward a message", Tags: []string{"messages"},
		Description:  "Forward a message (inbound or outbound) to new recipients; the original is quoted and its attachments are carried over by default. Any attachments[] you supply are added on top of the originals. 202 when held for HITL.",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": held202(), "default": s.errorEnvelopeResponse()},
	}, s.handleForward)

	huma.Register(s.API, huma.Operation{
		OperationID: "testAgent", Method: http.MethodPost, Path: "/v1/agents/{email}/test",
		Summary: "Send a test email to the agent's own address", Tags: []string{"agents"},
		Description: "Send a platform test email to the agent's own address to confirm inbound delivery. 202 when held for HITL.",
		Security:    []map[string][]string{{"bearer": {}}},
		Responses:   map[string]*huma.Response{"202": held202(), "default": s.errorEnvelopeResponse()},
	}, s.handleTestSend)
}

func (s *Server) handleTestSend(ctx context.Context, in *AddressParam) (*sendOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, uerr
	}
	if env := s.checkSendLimit(ag.ID); env != nil {
		return nil, env
	}
	if !ag.DomainVerified {
		return nil, NewError(http.StatusForbidden, "domain_not_verified", "agent domain must be verified before sending test email")
	}
	if s.deps.EnforceMessageSend != nil {
		if err := s.deps.EnforceMessageSend(ctx, user.ID); err != nil {
			if env, ok := limitEnvelope(err); ok {
				return nil, env
			}
			return nil, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
		}
	}
	if s.deps.SendTest == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "test send unavailable")
	}
	res, derr := s.deps.SendTest(ctx, ag)
	if derr != nil {
		return nil, NewError(derr.Status, derr.Code, derr.Msg)
	}
	if res.Held {
		return &sendOutput{Status: http.StatusAccepted, Body: SendResultView{Status: "pending_review", MessageID: res.PendingMessageID, ApprovalExpiresAt: res.ApprovalExpiresAt}}, nil
	}
	return &sendOutput{Status: http.StatusOK, Body: SendResultView{Status: "sent", MessageID: res.MessageID, ProviderMessageID: res.ProviderMessageID, SentAs: res.SentAs, Method: res.Method}}, nil
}

// ReplyRequest mirrors the legacy reply body.
type ReplyRequest struct {
	Body           string                `json:"body"` // required (MSG-3); to/subject derived from the original
	HTMLBody       string                `json:"html_body,omitempty"`
	ReplyAll       bool                  `json:"reply_all,omitempty"`
	CC             []string              `json:"cc,omitempty" nullable:"false"`
	BCC            []string              `json:"bcc,omitempty" nullable:"false"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false"`
}

type replyInput struct {
	Address        string `path:"email"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Wait           string `query:"wait" doc:"Sync-compat valve. wait=sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status=accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome."`
	Body           ReplyRequest
}

// loadRepliableMessage resolves the owned agent + the reply/forward target
// message — inbound or outbound — (404 if missing, expired/held, or not on
// this agent).
func (s *Server) loadRepliableMessage(ctx context.Context, address, msgID string) (*identity.AgentIdentity, *identity.Message, *identity.User, error) {
	ag, err := s.resolveOwnedAgent(ctx, address)
	if err != nil {
		return nil, nil, nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, nil, nil, uerr
	}
	if s.deps.GetRepliableMessage == nil {
		return nil, nil, nil, NewError(http.StatusInternalServerError, "internal_error", "outbound unavailable")
	}
	msg, err := s.deps.GetRepliableMessage(ctx, msgID)
	if err != nil || msg == nil || msg.AgentID != ag.ID {
		return nil, nil, nil, NewError(http.StatusNotFound, "not_found", "message not found")
	}
	return ag, msg, user, nil
}

func (s *Server) handleReply(ctx context.Context, in *replyInput) (*sendOutput, error) {
	ag, msg, user, err := s.loadRepliableMessage(ctx, in.Address, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if b.Body == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "body is required")
	}
	// Validate only the user-supplied CC/BCC; the implicit To comes from the
	// (already-validated) referenced message — mirrors the legacy handler.
	if env := recipientCountError(b.CC, b.BCC); env != nil {
		return nil, env
	}
	if e := agent.ValidateRecipients(b.CC, b.BCC); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", e.Error())
	}
	if e := validateConversationID(b.ConversationID); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", e.Error())
	}
	// Build the reply request via the same outbound helpers the legacy
	// handler uses (subject normalization, recipient parsing, References).
	subject := msg.Subject
	if subject != "" && !strings.HasPrefix(strings.ToLower(subject), "re: ") {
		subject = "Re: " + subject
	} else if subject == "" {
		subject = "Re: your message"
	}
	// Recipient derivation branches on direction. Replying to a message the
	// agent RECEIVED targets its sender (Reply-To/From). Replying to a message
	// the agent SENT continues the thread to its original recipients (To, plus
	// Cc on reply_all) — reply-to-From would just address the agent itself.
	// BCC is never carried in either case.
	rr, e := s.replyRecipients(msg, b.ReplyAll, b.CC)
	if e != nil {
		return nil, e
	}
	// Anchor threading on the parent's RFC Message-ID. For an inbound that's the
	// sender's Message-ID (email_message_id); for the agent's own outbound it's
	// the relay-assigned provider_message_id — email_message_id is empty there,
	// so using it would drop In-Reply-To/References and fork the recipient's
	// thread (see identity.Message.ThreadMessageID).
	parentMessageID := msg.ThreadMessageID()
	req := outbound.SendRequest{
		To: rr.To, CC: rr.CC, BCC: b.BCC, Subject: subject, Body: b.Body, HTMLBody: b.HTMLBody,
		ReplyToMessageID: parentMessageID,
		References:       outbound.BuildReferencesChain(msg.RawMessage, parentMessageID),
		// conversation_id resolution (caller id > inherit-from-referenced > mint)
		// is centralized in DeliverOutbound, which receives this message as the
		// referenced message — so the reply inherits its thread there (#328).
		ConversationID: b.ConversationID, Attachments: b.Attachments,
	}
	req.CC = agent.StripAgentSelfAliases(req.CC, ag.EmailAddress())
	req.BCC = agent.StripAgentSelfAliases(req.BCC, ag.EmailAddress())
	// Re-count the FINAL, post-expansion recipient set. reply_all fans the
	// thread's To+Cc into req.To/req.CC above, so the earlier b.CC/b.BCC check is
	// not the real fan-out — without this, a reply_all to a large thread bypasses
	// the cap that /send and /forward enforce (the downstream send path has no
	// cap of its own).
	if env := recipientCountError(req.To, req.CC, req.BCC); env != nil {
		return nil, env
	}
	return s.deliver(ctx, user, ag, literalRequest(req), "reply", parentMessageID, "/v1/reply/"+in.ID, in.IdempotencyKey, in.RawBody, msg)
}

// replyRecipients resolves a reply's To/CC from the referenced message,
// branching on direction. An inbound (received) message replies to its sender;
// an outbound (sent) message continues the thread to its original recipients.
// Returns a 400 envelope for an outbound target with no recorded recipients —
// falling back to the message's Sender there would address the agent itself, so
// we fail closed rather than emit a self-addressed reply.
func (s *Server) replyRecipients(msg *identity.Message, replyAll bool, extraCC []string) (*outbound.ReplyRecipients, *ErrorEnvelope) {
	if msg.Direction == "outbound" {
		rr := outbound.ReplyRecipientsForOutbound(msg.ToRecipients, msg.CC, extraCC, replyAll)
		if len(rr.To) == 0 {
			return nil, NewError(http.StatusBadRequest, "invalid_recipient",
				"cannot reply: the original message has no recorded recipients")
		}
		return rr, nil
	}
	rr, err := outbound.ParseReplyRecipients(msg.RawMessage, replyAll, extraCC)
	if err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", err.Error())
	}
	if len(rr.To) == 0 {
		rr.To = []string{msg.Sender}
	}
	return rr, nil
}

// ForwardRequest mirrors the legacy forward body.
type ForwardRequest struct {
	To             []string              `json:"to" nullable:"false"` // required (MSG-3)
	CC             []string              `json:"cc,omitempty" nullable:"false"`
	BCC            []string              `json:"bcc,omitempty" nullable:"false"`
	Body           string                `json:"body"` // required (MSG-3); subject derived as "Fwd:"
	HTMLBody       string                `json:"html_body,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false" doc:"Additional attachments to include alongside the forwarded message's original attachments, which are carried over automatically."`
}

type forwardInput struct {
	Address        string `path:"email"`
	ID             string `path:"id"`
	RawBody        []byte
	IdempotencyKey string `header:"Idempotency-Key"`
	Wait           string `query:"wait" doc:"Sync-compat valve. wait=sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status=accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome."`
	Body           ForwardRequest
}

func (s *Server) handleForward(ctx context.Context, in *forwardInput) (*sendOutput, error) {
	ag, msg, user, err := s.loadRepliableMessage(ctx, in.Address, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if len(b.To) == 0 && len(b.CC) == 0 {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "at least one recipient in to or cc is required")
	}
	if env := recipientCountError(b.To, b.CC, b.BCC); env != nil {
		return nil, env
	}
	if e := agent.ValidateRecipients(b.To, b.CC, b.BCC); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_recipient", e.Error())
	}
	if e := validateConversationID(b.ConversationID); e != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", e.Error())
	}
	subject := outbound.BuildForwardSubject(msg.Subject)
	fwdCtx := outbound.ExtractForwardContext(msg.RawMessage)
	composedBody := outbound.BuildForwardBody(b.Body, fwdCtx)
	var composedHTML string
	if b.HTMLBody != "" || fwdCtx.HTML != "" || fwdCtx.Text != "" {
		composedHTML = outbound.BuildForwardHTMLBody(b.HTMLBody, fwdCtx)
	}
	// Carry the source message's attachment parts by default (#298): a
	// forward should ship the original files the way mail clients do, without
	// the caller re-fetching and re-encoding each one. Caller-supplied
	// attachments are additive on top of the originals.
	attachments := outbound.ForwardAttachments(msg.RawMessage)
	attachments = append(attachments, b.Attachments...)
	req := outbound.SendRequest{
		To: b.To, CC: b.CC, BCC: b.BCC, Subject: subject, Body: composedBody, HTMLBody: composedHTML,
		ConversationID: b.ConversationID, Attachments: attachments,
	}
	req.CC = agent.StripAgentSelfAliases(req.CC, ag.EmailAddress())
	req.BCC = agent.StripAgentSelfAliases(req.BCC, ag.EmailAddress())
	return s.deliver(ctx, user, ag, literalRequest(req), "forward", msg.ThreadMessageID(), "/v1/forward/"+in.ID, in.IdempotencyKey, in.RawBody, msg)
}

// validateOutboundBody runs the shared pre-send validation.
func (s *Server) validateOutboundBody(subject, body string, to, cc, bcc []string, conversationID string) *ErrorEnvelope {
	if subject == "" || body == "" {
		return NewError(http.StatusBadRequest, "invalid_request", "subject and body are required")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return NewError(http.StatusBadRequest, "invalid_request", "subject must not contain CR or LF characters")
	}
	if len(to) == 0 && len(cc) == 0 {
		return NewError(http.StatusBadRequest, "invalid_request", "at least one recipient in to or cc is required")
	}
	if env := recipientCountError(to, cc, bcc); env != nil {
		return env
	}
	if err := agent.ValidateRecipients(to, cc, bcc); err != nil {
		return NewError(http.StatusBadRequest, "invalid_recipient", err.Error())
	}
	if err := validateConversationID(conversationID); err != nil {
		return NewError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return nil
}

// validateAttachments rejects any attachment whose Data is not decodable
// base64. The composer passes att.Data through verbatim into the MIME body
// (Content-Transfer-Encoding: base64), so malformed base64 otherwise slips past
// every check and only fails downstream at the SMTP relay — surfacing to the
// caller as a generic 500 instead of a clear 400. Whitespace (line-wrapping) is
// stripped first to match how mail decoders treat base64 bodies, so a caller
// that pre-wraps its base64 is not falsely rejected.
func validateAttachments(atts []outbound.Attachment) *ErrorEnvelope {
	for i, att := range atts {
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, att.Data)
		if _, err := base64.StdEncoding.DecodeString(clean); err != nil {
			name := att.Filename
			if name == "" {
				name = fmt.Sprintf("#%d", i)
			}
			return NewError(http.StatusBadRequest, "invalid_attachment",
				fmt.Sprintf("attachment %q: data is not valid base64", name))
		}
	}
	return nil
}

// deliver runs the idempotency handshake, then — inside the claimed
// execution — builds the request via prepare, runs the send-limit /
// domain-verified / enforce-cap checks, and calls DeliverOutbound, mapping
// the OutboundResult to the wire view.
//
// Everything that consults MUTABLE state (template resolution inside
// prepare, rate limits, plan caps) runs after the Claim so a keyed retry
// replays the cached response instead of re-evaluating state that may have
// changed since the first attempt (deleted template, exhausted quota, …).
// Failures inside the closure happen strictly before the DeliverOutbound
// side effect, so runIdempotent releases the key and a retry can proceed —
// exactly fn's documented contract.
func (s *Server) deliver(ctx context.Context, user *identity.User, ag *identity.AgentIdentity, prepare func() (outbound.SendRequest, *ErrorEnvelope), msgType, replyTo, route, idemKey string, rawBody []byte, referenced *identity.Message) (*sendOutput, error) {
	if s.deps.DeliverOutbound == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "outbound delivery unavailable")
	}
	status, view, err := runIdempotent(s, ctx, user.ID, idemKey, route, rawBody, func() (int, SendResultView, error) {
		req, env := prepare()
		if env != nil {
			return 0, SendResultView{}, env
		}
		if env := validateAttachments(req.Attachments); env != nil {
			return 0, SendResultView{}, env
		}
		if env := s.checkSendLimit(ag.ID); env != nil {
			return 0, SendResultView{}, env
		}
		if !ag.DomainVerified {
			return 0, SendResultView{}, NewError(http.StatusForbidden, "domain_not_verified", "agent domain must be verified before sending")
		}
		if s.deps.EnforceMessageSend != nil {
			if err := s.deps.EnforceMessageSend(ctx, user.ID); err != nil {
				if env, ok := limitEnvelope(err); ok {
					return 0, SendResultView{}, env
				}
				return 0, SendResultView{}, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
			}
		}
		res, derr := s.deps.DeliverOutbound(ctx, user, ag, req, msgType, replyTo, referenced)
		if derr != nil {
			return 0, SendResultView{}, NewError(derr.Status, derr.Code, derr.Msg)
		}
		if res.Held {
			return http.StatusAccepted, SendResultView{Status: "pending_review", MessageID: res.PendingMessageID, ApprovalExpiresAt: res.ApprovalExpiresAt}, nil
		}
		return http.StatusOK, SendResultView{Status: "sent", MessageID: res.MessageID, ProviderMessageID: res.ProviderMessageID, SentAs: res.SentAs, Method: res.Method}, nil
	})
	if err != nil {
		return nil, err
	}
	return &sendOutput{Status: status, Body: view}, nil
}

// literalRequest wraps an already-built SendRequest as a deliver prepare
// closure — used by reply/forward, whose request is fully derived from the
// request bytes plus the (already-loaded) referenced message.
func literalRequest(req outbound.SendRequest) func() (outbound.SendRequest, *ErrorEnvelope) {
	return func() (outbound.SendRequest, *ErrorEnvelope) { return req, nil }
}

// checkSendLimit applies the per-agent outbound rate limit (mirrors the
// legacy sendLimit). On block it returns a 429 envelope carrying the
// retry-after seconds; the IETF RateLimit-* response headers are a tracked
// follow-up (Huma error responses can't set headers directly).
func (s *Server) checkSendLimit(agentID string) *ErrorEnvelope {
	if s.deps.SendLimit == nil {
		return nil
	}
	ok, retryAfter := s.deps.SendLimit(agentID)
	if ok {
		return nil
	}
	secs := int(retryAfter.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	return NewError(http.StatusTooManyRequests, "rate_limited",
		"rate limit exceeded — max 60 sends per minute per agent").
		WithDetails(map[string]any{"retry_after_seconds": secs})
}

func (s *Server) handleCreateMessage(ctx context.Context, in *createMessageInput) (*sendOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	user, uerr := s.requireUser(ctx)
	if uerr != nil {
		return nil, uerr
	}
	b := in.Body
	// The deterministic template-shape checks (mutual exclusions) depend only
	// on the request bytes, so they stay in the prologue. Resolution +
	// rendering consult the mutable templates table and therefore run inside
	// the idempotent execution (in prepare, below).
	if env := validateSendTemplateShape(&b); env != nil {
		return nil, env
	}
	// The agent moved from the body (`from`) to the path, so fold the agent id
	// into the idempotency route — otherwise two agents owned by the same user
	// could collide on an identical key+body (the body hash alone no longer
	// separates them).
	route := "/v1/agents/" + ag.ID + "/messages"
	prepare := func() (outbound.SendRequest, *ErrorEnvelope) {
		// Resolve + render any template reference FIRST (in place), so the
		// rendered subject/body flow through the exact same validation below
		// and any HITL hold persists rendered content (see resolveSendTemplate
		// for both ordering invariants: after the idempotency claim, before
		// the hold).
		if env := s.resolveSendTemplate(ctx, user.ID, &b); env != nil {
			return outbound.SendRequest{}, env
		}
		if env := s.validateOutboundBody(b.Subject, b.Body, b.To, b.CC, b.BCC, b.ConversationID); env != nil {
			return outbound.SendRequest{}, env
		}
		// The sender is the path agent (decision 3) — there is no body `from`;
		// the agent is the path and auth scopes the sender, so no spoofing is
		// possible.
		return outbound.SendRequest{
			From: ag.EmailAddress(), To: b.To, CC: b.CC, BCC: b.BCC, Subject: b.Subject,
			Body: b.Body, HTMLBody: b.HTMLBody, ConversationID: b.ConversationID, Attachments: b.Attachments,
		}, nil
	}
	// A cold send has no referenced inbound (nil) — it's not a reply/forward.
	return s.deliver(ctx, user, ag, prepare, "send", "", route, in.IdempotencyKey, in.RawBody, nil)
}
