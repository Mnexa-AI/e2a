package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/mailparse"
	"github.com/danielgtaylor/huma/v2"
)

// MessageView is the full single-message representation. It mirrors the
// legacy GET /api/v1/agents/{email}/messages/{id} body field-for-field
// (Slice 1 is path move + conventions only — no shape change). All keys are
// emitted unconditionally to match the legacy map, including JSON null for
// absent cc/reply_to/auth_headers/raw_message. `status` carries the legacy
// delivery_status alias verbatim (the read_status rename is a later slice).
type MessageView struct {
	MessageID      string   `json:"message_id"`
	From           string   `json:"from"`
	To             []string `json:"to"`
	CC             []string `json:"cc"`
	ReplyTo        []string `json:"reply_to"`
	Recipient      string   `json:"recipient"`
	Subject        string   `json:"subject"`
	ConversationID string   `json:"conversation_id"`
	// Direction (inbound|outbound) — mirrors MessageSummaryView so a client
	// fetching a single message keeps the full trust-axis context (review F1).
	Direction string `json:"direction" enum:"inbound,outbound"`
	Status    string `json:"status"`
	// HITLStatus is the human-in-the-loop lifecycle (e.g. pending_approval) —
	// outbound only, mirroring MessageSummaryView. Distinct from `status`
	// (the delivery rollup) so a held draft is identifiable on the detail view
	// without re-deriving it (review F1). Closed set = migration 003 CHECK.
	HITLStatus string `json:"hitl_status,omitempty" enum:"pending_approval,sent,rejected,expired_approved,expired_rejected"`
	// DeliveryStatus is the outbound delivery rollup (migration 031:
	// 'sent', 'delivered', 'bounced', …) — the worst recipient status by
	// precedence. Outbound-only; omitted on inbound messages.
	DeliveryStatus string `json:"delivery_status,omitempty" enum:"queued,sent,delivered,bounced,complained,deferred,failed"`
	// DeliveryDetail is the human-readable diagnostic for the delivery
	// rollup (e.g. bounce sub-type / SMTP response). Outbound-only.
	DeliveryDetail string `json:"delivery_detail,omitempty"`
	// SentAs is the From identity actually used at relay accept time.
	// Outbound-only; omitted on inbound messages.
	SentAs string `json:"sent_as,omitempty" enum:"own_address,relay"`
	// Flagged + FlagReason carry the inbound ingestion verdict (migration 033 /
	// Slice 7): true when the agent's inbound_policy gate flagged this message
	// on arrival (still delivered). Inbound-relevant; omitted on unflagged rows.
	Flagged     bool              `json:"flagged,omitempty"`
	FlagReason  string            `json:"flag_reason,omitempty"`
	Labels      []string          `json:"labels"`
	CreatedAt   string            `json:"created_at" format:"date-time"`
	AuthHeaders map[string]string `json:"auth_headers"`
	// Auth is the structured inbound authentication verdict (SPF/DKIM/DMARC,
	// each with status + detail) from migration 032. Inbound-only; omitted on
	// outbound messages, which carry no verdict.
	Auth       *emailauth.Result `json:"auth,omitempty"`
	RawMessage []byte            `json:"raw_message"`
	// Parsed is the injection-reduced view (decision 9 / Slice 4b-3): the raw
	// message rendered to text with quoted reply/forward chains stripped and a
	// length cap, for the agent to feed a model by default. Inbound-only; a
	// CONVENIENCE — `raw_message` is always present and the security decision is
	// made on `auth` + provenance, never on this stripped text.
	Parsed *MessageParsedView `json:"parsed,omitempty"`
	// Body is the mutable draft body for a held outbound message
	// (status=pending_approval), which has no raw_message yet. This is the
	// second representation the unified read exposes (decision 9): held drafts
	// carry body_text/body_html, sent/inbound carry raw_message. Omitted when
	// empty (sent/inbound rows).
	Body *MessageBodyView `json:"body,omitempty"`
}

// MessageParsedView is the parsed-body payload (see MessageView.Parsed).
type MessageParsedView struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// MessageBodyView is the held-draft body (see MessageView.Body).
type MessageBodyView struct {
	Text string `json:"text,omitempty"`
	HTML string `json:"html,omitempty"`
}

func messageViewFromIdentity(m *identity.Message) MessageView {
	v := MessageView{
		MessageID:      m.ID,
		From:           m.Sender,
		To:             orEmptyStrings(m.ToRecipients),
		CC:             m.CC,
		ReplyTo:        m.ReplyTo,
		Recipient:      m.Recipient,
		Subject:        m.Subject,
		ConversationID: m.ConversationID,
		Direction:      m.Direction,
		// `status` is the inbox read-state, identical to the summary view (B2);
		// the outbound delivery rollup lives in `delivery_status`, the HITL
		// lifecycle in `hitl_status`. (The store resolves m.DeliveryStatus to
		// inbox_status for inbound and the rollup for outbound, so the detail
		// view must read InboxStatus to agree with the summary.)
		Status: m.InboxStatus,
		Labels: orEmptyStrings(m.Labels),
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
		AuthHeaders:    m.AuthHeaders,
		Auth:           m.Auth,
		RawMessage:     m.RawMessage,
		Flagged:        m.Flagged,
		FlagReason:     m.FlagReason,
	}
	// Outbound delivery feedback (migration 031). On outbound rows
	// identity.Message.DeliveryStatus carries the delivery rollup; on
	// inbound rows it carries inbox_status, so these stay empty there.
	if m.Direction == "outbound" {
		v.DeliveryStatus = m.DeliveryStatus
		v.DeliveryDetail = m.DeliveryDetail
		v.SentAs = m.SentAs
		// HITL lifecycle (status column) — outbound only, mirroring the summary
		// view; on inbound rows `status` is not the HITL value (review F1).
		v.HITLStatus = m.Status
	}
	// Parsed view (decision 9): inbound-only, derived from the raw message.
	if m.Direction == "inbound" && len(m.RawMessage) > 0 {
		text, truncated := mailparse.ParsedBody(m.RawMessage, mailparse.DefaultMaxBytes)
		v.Parsed = &MessageParsedView{Text: text, Truncated: truncated}
	}
	// Held-draft body (decision 9 unification): the second representation a
	// pending_approval outbound message carries instead of raw_message. Gated on
	// outbound direction so it can never surface on an inbound row even if a
	// future load path populates the body columns.
	if m.Direction == "outbound" && (m.BodyText != "" || m.BodyHTML != "") {
		v.Body = &MessageBodyView{Text: m.BodyText, HTML: m.BodyHTML}
	}
	return v
}

// MessageIDParam is the path input for single-message operations.
type MessageIDParam struct {
	Address   string `path:"address" doc:"The agent's full email address."`
	MessageID string `path:"id" doc:"The message id, e.g. msg_abc123."`
}

type messageOutput struct {
	Body MessageView
}

// MessageSummaryView is the lightweight list representation. It mirrors the
// legacy messageSummary json shape field-for-field (Slice 1 keeps the item
// shape; only the *pagination envelope* changes to the standardized
// items/next_cursor — §4 decision 7). Replicated here rather than imported
// from the legacy agent package so the new layer carries no backwards
// dependency on the surface it replaces; it moves home when legacy is
// deleted at the 1Z cutover.
type MessageSummaryView struct {
	ID             string   `json:"message_id"`
	Direction      string   `json:"direction" enum:"inbound,outbound"`
	From           string   `json:"from"`
	To             []string `json:"to"`
	CC             []string `json:"cc,omitempty"`
	ReplyTo        []string `json:"reply_to,omitempty"`
	Recipient      string   `json:"recipient"`
	Subject        string   `json:"subject"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Status         string   `json:"status"`
	HITLStatus     string   `json:"hitl_status,omitempty" enum:"pending_approval,sent,rejected,expired_approved,expired_rejected"`
	WebhookStatus  string   `json:"webhook_status,omitempty"`
	WebhookError   string   `json:"webhook_error,omitempty"`
	// DeliveryStatus / DeliveryDetail / SentAs are the outbound delivery
	// rollup (migration 031). Outbound-only; omitted on inbound rows.
	DeliveryStatus string `json:"delivery_status,omitempty" enum:"queued,sent,delivered,bounced,complained,deferred,failed"`
	DeliveryDetail string `json:"delivery_detail,omitempty"`
	SentAs         string `json:"sent_as,omitempty" enum:"own_address,relay"`
	// Flagged + FlagReason are the inbound ingestion verdict (migration 033 /
	// Slice 7). Surfaced in list views so flagged mail is visible without a
	// per-message drill-down. Inbound-relevant; omitted on unflagged rows.
	Flagged    bool     `json:"flagged,omitempty"`
	FlagReason string   `json:"flag_reason,omitempty"`
	SizeBytes  int      `json:"size_bytes,omitempty"`
	Labels     []string `json:"labels"`
	CreatedAt  string   `json:"created_at" format:"date-time"`
	// Auth is the structured inbound authentication verdict (migration 032).
	// Inbound-only; omitted on outbound rows.
	Auth *emailauth.Result `json:"auth,omitempty"`
}

func messageSummaryFromIdentity(m identity.Message) MessageSummaryView {
	s := MessageSummaryView{
		ID:             m.ID,
		Direction:      m.Direction,
		From:           m.Sender,
		To:             orEmptyStrings(m.ToRecipients),
		CC:             m.CC,
		ReplyTo:        m.ReplyTo,
		Recipient:      m.Recipient,
		Subject:        m.Subject,
		ConversationID: m.ConversationID,
		Status:         m.InboxStatus,
		SizeBytes:      m.SizeBytes,
		Labels:         orEmptyStrings(m.Labels),
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
		Auth:           m.Auth,
		Flagged:        m.Flagged,
		FlagReason:     m.FlagReason,
	}
	if m.Direction == "outbound" {
		s.HITLStatus = m.Status
		s.WebhookStatus = m.WebhookStatus
		s.WebhookError = m.WebhookError
		// On outbound rows identity.Message.DeliveryStatus carries the
		// delivery rollup (migration 031); inbound rows carry inbox_status,
		// already surfaced as Status above.
		s.DeliveryStatus = m.DeliveryStatus
		s.DeliveryDetail = m.DeliveryDetail
		s.SentAs = m.SentAs
	}
	return s
}

// ListMessagesInput is the typed query surface for the message list. Cursor
// pagination (cursor/limit) replaces the legacy page_size/token (§4
// decision 7); the filters preserve legacy semantics.
type ListMessagesInput struct {
	Address         string   `path:"address"`
	Direction       string   `query:"direction" enum:"inbound,outbound,all" doc:"Defaults to inbound."`
	Status          string   `query:"status" enum:"unread,read,all" doc:"Inbound only. Defaults to unread for inbound, all otherwise."`
	Sort            string   `query:"sort" enum:"asc,desc" doc:"Defaults to desc (newest first)."`
	From            string   `query:"from" doc:"Case-insensitive substring match on sender."`
	SubjectContains string   `query:"subject_contains" doc:"Case-insensitive substring match on subject."`
	ConversationID  string   `query:"conversation_id"`
	Labels          []string `query:"labels" doc:"Repeatable; AND-matched."`
	Since           string   `query:"since" doc:"RFC3339; created_at >= since."`
	Until           string   `query:"until" doc:"RFC3339; created_at < until."`
	Cursor          string   `query:"cursor"`
	Limit           int      `query:"limit" minimum:"1" maximum:"100" default:"50"`
}

type listMessagesOutput struct {
	Body Page[MessageSummaryView]
}

// messagesCursor is the opaque continuation payload. It captures the last
// row's position plus the full filter identity so a continuation request
// can't silently change the result set under the cursor.
type messagesCursor struct {
	CreatedAt       time.Time `json:"c"`
	ID              string    `json:"i"`
	Status          string    `json:"s"`
	Direction       string    `json:"d"`
	AgentID         string    `json:"a"`
	Sort            string    `json:"so"`
	From            string    `json:"f,omitempty"`
	SubjectContains string    `json:"sc,omitempty"`
	ConversationID  string    `json:"cv,omitempty"`
	Since           string    `json:"sn,omitempty"`
	Until           string    `json:"un,omitempty"`
	Labels          []string  `json:"lb,omitempty"`
}

func (s *Server) registerMessages() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listMessages",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{address}/messages",
		Summary:     "List messages",
		Description: "List an agent's messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_approval.",
		Tags:        []string{"messages"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListMessages)

	huma.Register(s.API, huma.Operation{
		OperationID: "getMessage",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{address}/messages/{id}",
		Summary:     "Get a message",
		Description: "Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.",
		Tags:        []string{"messages"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, func(ctx context.Context, in *MessageIDParam) (*messageOutput, error) {
		ag, err := s.resolveOwnedAgent(ctx, in.Address)
		if err != nil {
			return nil, err
		}
		if s.deps.GetMessage == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "message lookup unavailable")
		}
		msg, err := s.deps.GetMessage(ctx, in.MessageID, ag.ID)
		if err != nil || msg == nil {
			return nil, NewError(http.StatusNotFound, "not_found", "message not found")
		}
		return &messageOutput{Body: messageViewFromIdentity(msg)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "updateMessage",
		Method:      http.MethodPatch,
		Path:        "/v1/agents/{address}/messages/{id}",
		Summary:     "Update a message (labels)",
		Description: "Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.",
		Tags:        []string{"messages"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleUpdateMessage)
}

// UpdateMessageRequest is the labels-delta body for PATCH …/messages/{id}.
// A label in both add and remove is removed (remove wins, per the store).
type UpdateMessageRequest struct {
	AddLabels    []string `json:"add_labels,omitempty"`
	RemoveLabels []string `json:"remove_labels,omitempty"`
}

type updateMessageInput struct {
	Address string `path:"address"`
	ID      string `path:"id"`
	Body    UpdateMessageRequest
}

// UpdateMessageResultView echoes the post-update label set so callers can
// reflect state without a follow-up fetch.
type UpdateMessageResultView struct {
	MessageID string   `json:"message_id"`
	Labels    []string `json:"labels"`
}

type updateMessageOutput struct {
	Body UpdateMessageResultView
}

// handleUpdateMessage applies a labels delta (ports the legacy
// PATCH /api/v1/agents/{email}/messages/{id}). This is a per-agent operation,
// so an agent-scoped credential may label its own messages — it goes through
// resolveOwnedAgent (which pins an agent-scoped credential to its bound agent),
// NOT requireAccountScope. Label rules are validated via the shared
// agent.NormalizeAndValidateLabelList so they can't drift from the legacy
// surface; the store enforces the per-message cap.
func (s *Server) handleUpdateMessage(ctx context.Context, in *updateMessageInput) (*updateMessageOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.ModifyMessageLabels == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "label update unavailable")
	}
	add, verr := agent.NormalizeAndValidateLabelList(in.Body.AddLabels, "add")
	if verr != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", verr.Error())
	}
	remove, verr := agent.NormalizeAndValidateLabelList(in.Body.RemoveLabels, "remove")
	if verr != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", verr.Error())
	}
	final, err := s.deps.ModifyMessageLabels(ctx, in.ID, ag.ID, add, remove)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMessageNotFound):
			return nil, NewError(http.StatusNotFound, "not_found", "message not found")
		case errors.Is(err, identity.ErrLabelLimitExceeded):
			return nil, NewError(http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("label limit exceeded — a message may carry at most %d labels", identity.MaxLabelsPerMessage))
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to update labels")
		}
	}
	return &updateMessageOutput{Body: UpdateMessageResultView{MessageID: in.ID, Labels: orEmptyStrings(final)}}, nil
}

// handleListMessages ports the legacy list handler: same filter semantics
// and defaults, but the standardized cursor envelope. Validation failures
// return the machine-branchable error envelope.
func (s *Server) handleListMessages(ctx context.Context, in *ListMessagesInput) (*listMessagesOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.ListMessages == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "message list unavailable")
	}

	// Direction (default inbound for SDK back-compat).
	direction := in.Direction
	if direction == "" {
		direction = "inbound"
	}

	// Status default depends on direction; only meaningful for inbound.
	status := in.Status
	if status == "" {
		if direction == "inbound" {
			status = "unread"
		} else {
			status = "all"
		}
	}
	if direction == "outbound" && status != "all" {
		return nil, NewError(http.StatusBadRequest, "invalid_filter",
			"status filter only applies to inbound messages — pass status=all when direction=outbound")
	}

	// Bounded substring filters.
	if len(in.From) > maxFilterStr {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", "from filter too long (max 200 chars)")
	}
	if len(in.SubjectContains) > maxFilterStr {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", "subject_contains filter too long (max 200 chars)")
	}
	if len(in.ConversationID) > maxFilterStr {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", "conversation_id too long (max 200 chars)")
	}
	if err := validateConversationID(in.ConversationID); err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", err.Error())
	}

	// Labels filter: validate + dedup (read access allows the e2a: system
	// namespace, matching legacy allowSystemPrefix=true).
	labelsFilter, err := normalizeLabelFilter(in.Labels)
	if err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", err.Error())
	}

	// Time range.
	since, err := parseRFC3339Filter(in.Since, "since")
	if err != nil {
		return nil, err
	}
	until, err := parseRFC3339Filter(in.Until, "until")
	if err != nil {
		return nil, err
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		return nil, NewError(http.StatusBadRequest, "invalid_filter", "since must be earlier than until")
	}

	// Effective sort (default newest-first).
	sort := in.Sort
	if sort == "" {
		sort = "desc"
	}

	// Decode + validate the cursor against the current filter identity.
	var afterTime time.Time
	var afterID string
	if in.Cursor != "" {
		var cur messagesCursor
		if err := DecodeCursor(in.Cursor, &cur); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_cursor", "invalid pagination cursor")
		}
		if cur.AgentID != ag.ID || cur.Status != status || cur.Direction != direction || cur.Sort != sort ||
			cur.From != in.From || cur.SubjectContains != in.SubjectContains ||
			cur.ConversationID != in.ConversationID ||
			cur.Since != rfc3339OrEmpty(since) || cur.Until != rfc3339OrEmpty(until) ||
			!stringSlicesEqual(cur.Labels, labelsFilter) {
			return nil, NewError(http.StatusBadRequest, "invalid_cursor",
				"cursor was created with different filters — start a new query without a cursor")
		}
		afterTime = cur.CreatedAt
		afterID = cur.ID
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	// Fetch limit+1 to detect a further page.
	msgs, err := s.deps.ListMessages(ctx, identity.MessageListFilter{
		AgentID:         ag.ID,
		Status:          status,
		Direction:       direction,
		Descending:      sort == "desc",
		Limit:           limit + 1,
		AfterTime:       afterTime,
		AfterID:         afterID,
		From:            in.From,
		SubjectContains: in.SubjectContains,
		ConversationID:  in.ConversationID,
		Since:           since,
		Until:           until,
		Labels:          labelsFilter,
	})
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to fetch messages")
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}
	items := make([]MessageSummaryView, len(msgs))
	for i, m := range msgs {
		items[i] = messageSummaryFromIdentity(m)
	}

	var nextCursor string
	if hasMore {
		last := msgs[len(msgs)-1]
		nextCursor, err = EncodeCursor(messagesCursor{
			CreatedAt: last.CreatedAt, ID: last.ID,
			Status: status, Direction: direction, AgentID: ag.ID, Sort: sort,
			From: in.From, SubjectContains: in.SubjectContains, ConversationID: in.ConversationID,
			Since: rfc3339OrEmpty(since), Until: rfc3339OrEmpty(until), Labels: labelsFilter,
		})
		if err != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to build pagination cursor")
		}
	}

	return &listMessagesOutput{Body: NewPage(items, nextCursor)}, nil
}

// --- replicated, contract-stable validation helpers (see MessageSummaryView
// doc for why these live here rather than importing the legacy package) ---

// maxFilterStr bounds free-form query filters (mirrors the legacy cap).
const maxFilterStr = 200

// maxLabelLength / maxLabelsPerOp / labelSystemPrefix mirror the legacy
// label invariants verbatim so /v1 filter validation can't drift from the
// write-side charset rule that guards the GIN index.
const (
	maxLabelLength    = 64
	maxLabelsPerOp    = 50
	labelSystemPrefix = "e2a:"
)

func validateConversationID(id string) error {
	if strings.ContainsAny(id, "\r\n") {
		return errors.New("conversation_id must not contain CR or LF")
	}
	return nil
}

// normalizeLabel canonicalizes a single label (lowercase, charset
// [a-z0-9:_-], 1..maxLabelLength). allowSystem mirrors the read-side
// allowSystemPrefix=true: filtering by an e2a: system label is permitted.
func normalizeLabel(raw string, allowSystem bool) (string, error) {
	l := strings.ToLower(strings.TrimSpace(raw))
	if l == "" {
		return "", errors.New("label must not be empty")
	}
	if len(l) > maxLabelLength {
		return "", fmt.Errorf("label too long (max %d chars)", maxLabelLength)
	}
	for _, r := range l {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == ':':
		default:
			return "", fmt.Errorf("label %q has invalid character; allowed: a-z 0-9 : - _", l)
		}
	}
	if !allowSystem && strings.HasPrefix(l, labelSystemPrefix) {
		return "", fmt.Errorf("labels starting with %q are reserved for system use", labelSystemPrefix)
	}
	return l, nil
}

// normalizeLabelFilter validates + dedups a labels= filter list.
func normalizeLabelFilter(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxLabelsPerOp {
		return nil, fmt.Errorf("labels filter exceeds cap of %d", maxLabelsPerOp)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		l, err := normalizeLabel(r, true)
		if err != nil {
			return nil, fmt.Errorf("labels filter: %w", err)
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out, nil
}

// parseRFC3339Filter parses an optional RFC3339 timestamp query param into
// a time, returning a 400 envelope on a malformed value.
func parseRFC3339Filter(raw, name string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, NewError(http.StatusBadRequest, "invalid_filter",
			name+" must be RFC3339 (e.g. 2026-05-25T00:00:00Z)")
	}
	return t, nil
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// orEmptyStrings normalizes a nil slice to a non-nil empty slice so the
// field renders as [] rather than null — matching the legacy orEmptySlice
// behavior for `to` and `labels`.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
