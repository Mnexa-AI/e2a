package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"reflect"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
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

// limitExceededResponse is the typed 402 response for the cap-enforcing
// operations (create agent, register domain, send/reply/forward/test). Its
// schema is the LimitExceededEnvelope, whose error.details is a typed
// LimitExceededDetails so codegen surfaces a concrete shape (resource keyed to
// the AccountView usage/limits field stems) instead of a bare `any`.
func (s *Server) limitExceededResponse() *huma.Response {
	return s.jsonResponse(reflect.TypeOf(LimitExceededEnvelope{}), "LimitExceededEnvelope",
		"Payment required — a per-account resource cap was hit (code limit_exceeded). error.details.resource is the AccountView usage/limits field stem (agents, domains, messages_month, storage_bytes), so the client can key it to usage.<resource> / limits.max_<resource>.")
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

// maxOutboundBytes is the coarse WIRE-size backstop on the outbound request body
// (send/reply/forward): Huma reads at most this many raw bytes before parsing, so
// an unbounded body can't exhaust memory. It is deliberately larger than the
// decoded attachment limits below, because attachment bytes arrive base64-encoded
// on the wire (~33% larger than decoded): a request at the 25 MB DECODED total is
// ~33.3 MB of base64 plus the JSON envelope and text body. 40 MB admits any valid
// request with headroom while the real ceilings — the per-attachment / total
// DECODED limits — are enforced after decode in validateAttachments. (The legacy
// 25 MB value was on the WIRE, so it silently rejected legitimately-sized
// attachment payloads; that raw-vs-decoded mismatch is reconciled here.)
const maxOutboundBytes = 40 * 1024 * 1024

// Attachment limits, enforced on DECODED bytes (not the base64 wire size) across
// every outbound path that accepts attachments — send, reply, forward, and an
// approve that edits the held draft's attachments. Conservative starting values
// (GA freeze): raising a limit later is non-breaking, lowering is breaking, so we
// start small and leave headroom under the downstream ceiling. The combined total
// (25 MB decoded ≈ 33.3 MB base64 in the composed MIME) stays safely under the
// AWS SES 40 MB per-message ceiling.
const (
	// maxAttachmentBytes caps a single attachment's decoded size. Over → 413.
	maxAttachmentBytes = 10 * 1024 * 1024
	// maxAttachmentCount caps how many attachments one message may carry. Over → 400.
	maxAttachmentCount = 10
	// maxAttachmentsTotalBytes caps the combined decoded size of all attachments on
	// one message. Over → 413. Aligned to the whole-request budget and kept under
	// the SES 40 MB encoded ceiling once base64-inflated.
	maxAttachmentsTotalBytes = 25 * 1024 * 1024
)

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
// two mutually exclusive shapes: literal subject + text (+ optional
// html) — both required at the handler for a usable new email (MSG-3) —
// or a template reference (template_id XOR template_alias, + template_data),
// which the server renders into subject/text/html before any further
// processing. subject/text moved from schema-required to handler-enforced so
// the template shape can omit them.
type SendEmailRequest struct {
	To             []string              `json:"to" nullable:"false"`
	CC             []string              `json:"cc,omitempty" nullable:"false"`
	BCC            []string              `json:"bcc,omitempty" nullable:"false"`
	Subject        string                `json:"subject,omitempty" doc:"Literal subject. Required unless a template reference is used (mutually exclusive with template_id/template_alias)."`
	Body           string                `json:"text,omitempty" doc:"Literal plain-text body. Required unless a template reference is used (mutually exclusive with template_id/template_alias)."`
	HTMLBody       string                `json:"html,omitempty" doc:"Literal HTML body. Mutually exclusive with template_id/template_alias."`
	TemplateID     string                `json:"template_id,omitempty" doc:"Send using a stored template (rendered server-side, before any review hold). Mutually exclusive with template_alias and with literal subject/body/html_body. Beta: templates are unstable — their shape may change before they are declared stable."`
	TemplateAlias  string                `json:"template_alias,omitempty" doc:"Send using a stored template resolved by its per-user alias. Mutually exclusive with template_id and with literal subject/body/html_body. Beta: templates are unstable — their shape may change before they are declared stable."`
	TemplateData   TemplateData          `json:"template_data,omitempty" doc:"Variables for the referenced template ({{name}}, dot paths into nested objects). Missing variables render as empty strings. Beta: templates are unstable — their shape may change before they are declared stable."`
	ConversationID string                `json:"conversation_id,omitempty"`
	ReplyTo        string                `json:"reply_to,omitempty" doc:"Sets the Reply-To header — where replies to this message are directed. A single RFC 5322 address, optionally with a display name (e.g. \"Support <support@acme.com>\"). Defaults to the sending agent's own address."`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false" doc:"File attachments (base64 in each item's data). Limits: at most 10 attachments, each ≤ 10 MB decoded, and ≤ 25 MB decoded combined. Exceeding the count → 400 invalid_request; exceeding a size → 413 payload_too_large."`
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
	// 202 Accepted covers every non-terminal outbound outcome: the message was
	// durably accepted but not yet delivered — either queued for async
	// submission (status=accepted; the terminal sent/failed arrives via GET /
	// webhook events) or held for human approval (status=pending_review).
	// Declared explicitly because Huma infers only the single DefaultStatus
	// (200, kept for the terminal-synchronous status=sent result).
	accepted202 := func() *huma.Response {
		return s.jsonResponse(reflect.TypeOf(SendResultView{}), "SendResultView",
			"Accepted — durably accepted but not yet delivered: status=accepted (queued for async submission; terminal outcome via GET/webhook events) or status=pending_review (held for human approval).")
	}
	// 400 and 413 are declared explicitly on every attachment-bearing operation so
	// a client knows the failure modes up front. 400 invalid_request covers too many
	// attachments (> 10) and the other request-shape validations; 413 payload_too_large
	// covers a single attachment over 10 MB (decoded) or a combined total over 25 MB
	// (decoded). Both render the standard ErrorEnvelope — branch on error.code.
	badRequest400 := func() *huma.Response {
		return s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
			"Bad Request — request-shape/validation failure. error.code includes invalid_request (e.g. more than 10 attachments), too_many_recipients, invalid_recipient, invalid_attachment (undecodable base64).")
	}
	tooLarge413 := func() *huma.Response {
		return s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
			"Payload Too Large — an attachment exceeds 10 MB decoded, or the combined attachments exceed 25 MB decoded. error.code = payload_too_large; error.details carries the offending size and the limit.")
	}

	huma.Register(s.API, huma.Operation{
		OperationID: "sendMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages",
		Summary: "Send a new email", Tags: []string{"messages"},
		Description:  "Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": accepted202(), "400": badRequest400(), "402": s.limitExceededResponse(), "413": tooLarge413(), "default": s.errorEnvelopeResponse()},
	}, s.handleCreateMessage)

	huma.Register(s.API, huma.Operation{
		OperationID: "replyToMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/reply",
		Summary: "Reply to a message", Tags: []string{"messages"},
		Description:  "Reply to a message (inbound or outbound); recipients and threading are derived from the original. Replying to a message the agent received targets its sender; replying to a message the agent sent continues the thread to its original recipients (`reply_all` also re-includes the original Cc). 202 when held for HITL. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": accepted202(), "400": badRequest400(), "402": s.limitExceededResponse(), "413": tooLarge413(), "default": s.errorEnvelopeResponse()},
	}, s.handleReply)

	huma.Register(s.API, huma.Operation{
		OperationID: "forwardMessage", Method: http.MethodPost, Path: "/v1/agents/{email}/messages/{id}/forward",
		Summary: "Forward a message", Tags: []string{"messages"},
		Description:  "Forward a message (inbound or outbound) to new recipients; the original is quoted and its attachments are carried over by default. Any attachments[] you supply are added on top of the originals. 202 when held for HITL. Attachment limits apply to the combined set (carried-over originals + supplied): at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).",
		Security:     []map[string][]string{{"bearer": {}}},
		MaxBodyBytes: maxOutboundBytes,
		Responses:    map[string]*huma.Response{"202": accepted202(), "400": badRequest400(), "402": s.limitExceededResponse(), "413": tooLarge413(), "default": s.errorEnvelopeResponse()},
	}, s.handleForward)

	huma.Register(s.API, huma.Operation{
		OperationID: "testAgent", Method: http.MethodPost, Path: "/v1/agents/{email}/test",
		Summary: "Send a test email to the agent's own address", Tags: []string{"agents"},
		Description: "Send a platform test email to the agent's own address to confirm inbound delivery. 202 when held for HITL.",
		Security:    []map[string][]string{{"bearer": {}}},
		Responses:   map[string]*huma.Response{"202": accepted202(), "402": s.limitExceededResponse(), "default": s.errorEnvelopeResponse()},
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
	Body           string                `json:"text"` // required (MSG-3); to/subject derived from the original
	HTMLBody       string                `json:"html,omitempty"`
	ReplyAll       bool                  `json:"reply_all,omitempty"`
	CC             []string              `json:"cc,omitempty" nullable:"false"`
	BCC            []string              `json:"bcc,omitempty" nullable:"false"`
	ConversationID string                `json:"conversation_id,omitempty"`
	ReplyTo        string                `json:"reply_to,omitempty" doc:"Sets the Reply-To header — where replies to this message are directed. A single RFC 5322 address, optionally with a display name. Defaults to the sending agent's own address."`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false" doc:"File attachments (base64 in each item's data). Limits: at most 10 attachments, each ≤ 10 MB decoded, and ≤ 25 MB decoded combined. Exceeding the count → 400 invalid_request; exceeding a size → 413 payload_too_large."`
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
		return nil, NewError(http.StatusBadRequest, "invalid_request", "text is required")
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
	if env := validateReplyTo(b.ReplyTo); env != nil {
		return nil, env
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
		ConversationID: b.ConversationID, ReplyTo: b.ReplyTo, Attachments: b.Attachments,
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
	return s.deliver(ctx, user, ag, literalRequest(req), "reply", parentMessageID, "/v1/reply/"+in.ID, in.IdempotencyKey, in.Wait, in.RawBody, msg)
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
	Body           string                `json:"text"` // required (MSG-3); subject derived as "Fwd:"
	HTMLBody       string                `json:"html,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	ReplyTo        string                `json:"reply_to,omitempty" doc:"Sets the Reply-To header — where replies to this message are directed. A single RFC 5322 address, optionally with a display name. Defaults to the sending agent's own address."`
	Attachments    []outbound.Attachment `json:"attachments,omitempty" nullable:"false" doc:"Additional attachments to include alongside the forwarded message's original attachments, which are carried over automatically. Limits apply to the combined set (originals + these): at most 10 attachments, each ≤ 10 MB decoded, and ≤ 25 MB decoded combined. Exceeding the count → 400 invalid_request; exceeding a size → 413 payload_too_large."`
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
	if env := validateReplyTo(b.ReplyTo); env != nil {
		return nil, env
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
		ConversationID: b.ConversationID, ReplyTo: b.ReplyTo, Attachments: attachments,
	}
	req.CC = agent.StripAgentSelfAliases(req.CC, ag.EmailAddress())
	req.BCC = agent.StripAgentSelfAliases(req.BCC, ag.EmailAddress())
	return s.deliver(ctx, user, ag, literalRequest(req), "forward", msg.ThreadMessageID(), "/v1/forward/"+in.ID, in.IdempotencyKey, in.Wait, in.RawBody, msg)
}

// validateOutboundBody runs the shared pre-send validation.
func (s *Server) validateOutboundBody(subject, body string, to, cc, bcc []string, conversationID string) *ErrorEnvelope {
	if subject == "" || body == "" {
		return NewError(http.StatusBadRequest, "invalid_request", "subject and text are required")
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

// validateReplyTo checks a caller-supplied Reply-To override. Empty is valid
// (the compose layer defaults it to the agent's own address). A non-empty value
// must be exactly one RFC 5322 address, optionally with a display name; multiple
// addresses and unparseable input are rejected at the edge so a bad Reply-To
// never reaches the composer (where sanitizeHeaderValue would silently mangle
// it) or the SMTP relay (a generic 500).
func validateReplyTo(replyTo string) *ErrorEnvelope {
	if replyTo == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(replyTo)
	if err != nil {
		return NewError(http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("reply_to is not a valid email address: %v", err))
	}
	if len(addrs) != 1 {
		return NewError(http.StatusBadRequest, "invalid_request",
			"reply_to must be a single email address")
	}
	return nil
}

// validateAttachments enforces the attachment contract on every outbound path
// (send/reply/forward, and approve-with-edits). It checks, in order:
//
//   - count ≤ maxAttachmentCount → 400 invalid_request (too many is a shape/
//     validation error, not an oversize payload)
//   - each att.Data is decodable base64 → 400 invalid_attachment (the composer
//     passes att.Data verbatim into the MIME body with Content-Transfer-Encoding:
//     base64, so malformed base64 would otherwise slip past every check and only
//     fail at the SMTP relay as a generic 500)
//   - each attachment's DECODED size ≤ maxAttachmentBytes → 413 payload_too_large
//   - the combined DECODED size ≤ maxAttachmentsTotalBytes → 413 payload_too_large
//
// All size checks are on DECODED bytes, not the base64 wire size, so the limits a
// caller sees are the real file sizes and match the bytes SES ultimately carries.
// Whitespace (line-wrapping) is stripped before decoding to match how mail decoders
// treat base64 bodies, so a caller that pre-wraps its base64 is not falsely rejected.
func validateAttachments(atts []outbound.Attachment) *ErrorEnvelope {
	if len(atts) > maxAttachmentCount {
		return NewError(http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("too many attachments — at most %d per message (got %d)", maxAttachmentCount, len(atts))).
			WithDetails(map[string]any{"max_attachments": maxAttachmentCount, "provided": len(atts)})
	}
	var total int
	for i, att := range atts {
		name := att.Filename
		if name == "" {
			name = fmt.Sprintf("#%d", i)
		}
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, att.Data)
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return NewError(http.StatusBadRequest, "invalid_attachment",
				fmt.Sprintf("attachment %q: data is not valid base64", name))
		}
		if len(decoded) > maxAttachmentBytes {
			return NewError(http.StatusRequestEntityTooLarge, "payload_too_large",
				fmt.Sprintf("attachment %q is too large — %d bytes decoded, limit is %d (%d MB)",
					name, len(decoded), maxAttachmentBytes, maxAttachmentBytes/(1024*1024))).
				WithDetails(map[string]any{
					"filename":             att.Filename,
					"decoded_bytes":        len(decoded),
					"max_attachment_bytes": maxAttachmentBytes,
				})
		}
		total += len(decoded)
	}
	if total > maxAttachmentsTotalBytes {
		return NewError(http.StatusRequestEntityTooLarge, "payload_too_large",
			fmt.Sprintf("attachments too large — %d bytes decoded in total, limit is %d (%d MB)",
				total, maxAttachmentsTotalBytes, maxAttachmentsTotalBytes/(1024*1024))).
			WithDetails(map[string]any{
				"total_decoded_bytes":         total,
				"max_attachments_total_bytes": maxAttachmentsTotalBytes,
			})
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
func (s *Server) deliver(ctx context.Context, user *identity.User, ag *identity.AgentIdentity, prepare func() (outbound.SendRequest, *ErrorEnvelope), msgType, replyTo, route, idemKey, wait string, rawBody []byte, referenced *identity.Message) (*sendOutput, error) {
	if s.deps.DeliverOutbound == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "outbound delivery unavailable")
	}
	// idemCompleteTx lets the async accept-tx (agent.DeliverOutbound) commit this
	// request's idempotency-key completion in the SAME transaction as the message
	// insert + send-job enqueue — so a crash after that commit replays 'accepted'
	// instead of re-persisting. It caches the EXACT wire body deliver() returns for
	// an accepted result (built below), keeping replay byte-faithful. nil when the
	// request carries no Idempotency-Key or no store is wired (then agent skips it,
	// and the synchronous path is unaffected). Uses the same user namespace + key
	// runIdempotent Claims/Completes under, so its in-tx Complete and runIdempotent's
	// post-hoc Complete address the same row (the latter no-ops on the in_progress
	// guard once this has run).
	var idemCompleteTx agent.AcceptIdemCompleter
	if idemKey != "" && s.deps.Idempotency != nil {
		nsKey := idemUserNS + idemKey
		uid := user.ID
		idemCompleteTx = func(ctx context.Context, tx pgx.Tx, messageID string) error {
			raw, mErr := json.Marshal(acceptedView(messageID))
			if mErr != nil {
				raw = []byte("{}")
			}
			return s.deps.Idempotency.CompleteTx(ctx, tx, uid, nsKey, idempotency.CachedResponse{
				StatusCode: http.StatusAccepted, ContentType: "application/json", Body: raw,
			})
		}
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
		res, derr := s.deps.DeliverOutbound(ctx, user, ag, req, msgType, replyTo, referenced, idemCompleteTx)
		if derr != nil {
			return 0, SendResultView{}, NewError(derr.Status, derr.Code, derr.Msg)
		}
		if res.Held {
			return http.StatusAccepted, SendResultView{Status: "pending_review", MessageID: res.PendingMessageID, ApprovalExpiresAt: res.ApprovalExpiresAt}, nil
		}
		// Async accept (slice C): 202 Accepted with status=accepted — the message
		// is durably persisted and queued for async submission; the terminal
		// outcome (sent/failed) arrives later via GET / webhooks, not this
		// response. The body MUST match acceptedView (the idempotency cache is
		// keyed to it) — no provider id / sent_as yet (the send hasn't happened).
		// The cached StatusCode (idemCompleteTx, above) is likewise 202 so a
		// replayed idempotent request returns the same 202, never a stale 200.
		if res.Status == "accepted" {
			return http.StatusAccepted, acceptedView(res.MessageID), nil
		}
		return http.StatusOK, SendResultView{Status: "sent", MessageID: res.MessageID, ProviderMessageID: res.ProviderMessageID, SentAs: res.SentAs, Method: res.Method}, nil
	})
	if err != nil {
		return nil, err
	}
	// wait=sent (contract §2): after an async accept, hold the request until the
	// send reaches sent/failed or the ceiling, then return that state. The
	// idempotency cache already holds the accept-time 'accepted' body (§2.4), so a
	// replay does NOT re-wait — only this live caller sees the polled outcome.
	if wait == "sent" && view.Status == "accepted" && s.deps.PollSendOutcome != nil {
		status, view = s.waitForSent(ctx, status, view)
	}
	return &sendOutput{Status: status, Body: view}, nil
}

const (
	waitSentCeiling = 15 * time.Second // below the 20s contract ceiling (§2.3) + proxy timeouts
	waitSentPoll    = 250 * time.Millisecond
)

// waitForSent polls the async send's delivery_status until sent/failed or the
// ceiling. Timeout → the accepted view (the caller polls GET / waits for the event).
func (s *Server) waitForSent(ctx context.Context, acceptedStatus int, accepted SendResultView) (int, SendResultView) {
	deadline := time.Now().Add(waitSentCeiling)
	for {
		if o, err := s.deps.PollSendOutcome(ctx, accepted.MessageID); err == nil {
			switch o.DeliveryStatus {
			case "sent", "delivered", "deferred", "bounced", "complained":
				return http.StatusOK, SendResultView{Status: "sent", MessageID: accepted.MessageID, ProviderMessageID: o.ProviderMessageID, SentAs: o.SentAs, Method: accepted.Method}
			case "failed":
				return http.StatusOK, SendResultView{Status: "failed", MessageID: accepted.MessageID, Method: accepted.Method}
			}
		}
		if time.Now().After(deadline) {
			return acceptedStatus, accepted // still in flight
		}
		select {
		case <-ctx.Done():
			return acceptedStatus, accepted
		case <-time.After(waitSentPoll):
		}
	}
}

// acceptedView is the single source of the async-accept wire body (slice C). Both
// the live response and the idempotency cache entry are built from it, so a replay
// is byte-identical. Deliberately minimal — status + message_id + method; the
// provider id / sent_as / delivery outcome are not known at accept time and surface
// later via GET /v1/messages/{id} and the email.sent / email.failed webhooks.
func acceptedView(messageID string) SendResultView {
	return SendResultView{Status: "accepted", MessageID: messageID, Method: "smtp"}
}

// literalRequest wraps an already-built SendRequest as a deliver prepare
// closure — used by reply/forward, whose request is fully derived from the
// request bytes plus the (already-loaded) referenced message.
func literalRequest(req outbound.SendRequest) func() (outbound.SendRequest, *ErrorEnvelope) {
	return func() (outbound.SendRequest, *ErrorEnvelope) { return req, nil }
}

// checkSendLimit applies the per-agent outbound rate limit (mirrors the
// legacy sendLimit). On block it returns a 429 envelope carrying the
// retry-after seconds in the body AND — via WithRetryAfter → stampRequestID —
// the IETF Retry-After response header, so a handler-raised send 429 matches
// the middleware-enforced registration/poll limiters (which set it directly).
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
		WithDetails(map[string]any{"retry_after_seconds": secs}).
		WithRetryAfter(secs)
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
		if env := validateReplyTo(b.ReplyTo); env != nil {
			return outbound.SendRequest{}, env
		}
		// The sender is the path agent (decision 3) — there is no body `from`;
		// the agent is the path and auth scopes the sender, so no spoofing is
		// possible.
		return outbound.SendRequest{
			From: ag.EmailAddress(), To: b.To, CC: b.CC, BCC: b.BCC, Subject: b.Subject,
			Body: b.Body, HTMLBody: b.HTMLBody, ConversationID: b.ConversationID,
			ReplyTo: b.ReplyTo, Attachments: b.Attachments,
		}, nil
	}
	// A cold send has no referenced inbound (nil) — it's not a reply/forward.
	return s.deliver(ctx, user, ag, prepare, "send", "", route, in.IdempotencyKey, in.Wait, in.RawBody, nil)
}
