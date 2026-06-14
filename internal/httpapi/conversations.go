package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// ConversationSummaryView mirrors the legacy conversationSummaryToWire shape
// (timestamps rendered as RFC3339 strings). Replicated here for the same
// reason as MessageSummaryView — no backwards dependency on the legacy
// package.
type ConversationSummaryView struct {
	ID             string `json:"conversation_id"`
	LastMessageAt  string `json:"last_message_at"`
	FirstMessageAt string `json:"first_message_at"`
	MessageCount   int    `json:"message_count"`
	InboundCount   int    `json:"inbound_count"`
	OutboundCount  int    `json:"outbound_count"`
	HasUnread      bool   `json:"has_unread"`
	LatestSubject  string `json:"latest_subject"`
	LatestSender   string `json:"latest_sender"`
}

func conversationSummaryView(c identity.ConversationSummary) ConversationSummaryView {
	return ConversationSummaryView{
		ID:             c.ID,
		LastMessageAt:  c.LastMessageAt.UTC().Format(time.RFC3339),
		FirstMessageAt: c.FirstMessageAt.UTC().Format(time.RFC3339),
		MessageCount:   c.MessageCount,
		InboundCount:   c.InboundCount,
		OutboundCount:  c.OutboundCount,
		HasUnread:      c.HasUnread,
		LatestSubject:  c.LatestSubject,
		LatestSender:   c.LatestSender,
	}
}

// ConversationDetailView is the single-conversation shape: the summary
// fields (flattened via embedding, matching the legacy top-level layout)
// plus participants, labels, and the member message summaries.
type ConversationDetailView struct {
	ConversationSummaryView
	Participants []string             `json:"participants"`
	Labels       []string             `json:"labels"`
	Messages     []MessageSummaryView `json:"messages"`
}

// ListConversationsInput — since/until + cursor/limit. Conversation cursor
// *continuation* is not yet supported by the store (it takes only a limit,
// no after-key), so next_cursor is always null — faithful to the legacy
// single-page behavior. True cursoring is a tracked follow-up needing a
// store change.
type ListConversationsInput struct {
	Address string `path:"address"`
	Since   string `query:"since" doc:"RFC3339."`
	Until   string `query:"until" doc:"RFC3339."`
	Limit   int    `query:"limit" minimum:"1" maximum:"200" default:"100"`
}

type listConversationsOutput struct {
	Body Page[ConversationSummaryView]
}

// ConversationIDParam is the path input for the single-conversation read.
type ConversationIDParam struct {
	Address        string `path:"address"`
	ConversationID string `path:"id"`
}

type conversationOutput struct {
	Body ConversationDetailView
}

func (s *Server) registerConversations() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listConversations",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{address}/conversations",
		Summary:     "List conversations",
		Description: "List an agent's conversation threads (derived from messages.conversation_id).",
		Tags:        []string{"conversations"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListConversations)

	huma.Register(s.API, huma.Operation{
		OperationID: "getConversation",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{address}/conversations/{id}",
		Summary:     "Get a conversation",
		Description: "Fetch a single conversation thread with its participants, labels, and member messages.",
		Tags:        []string{"conversations"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleGetConversation)
}

func (s *Server) handleListConversations(ctx context.Context, in *ListConversationsInput) (*listConversationsOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.ListConversations == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "conversation list unavailable")
	}
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
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	convos, err := s.deps.ListConversations(ctx, identity.ConversationListFilter{
		AgentID: ag.ID,
		Limit:   limit,
		Since:   since,
		Until:   until,
	})
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to fetch conversations")
	}
	items := make([]ConversationSummaryView, len(convos))
	for i, c := range convos {
		items[i] = conversationSummaryView(c)
	}
	// next_cursor always null — see ListConversationsInput doc.
	return &listConversationsOutput{Body: NewPage(items, "")}, nil
}

func (s *Server) handleGetConversation(ctx context.Context, in *ConversationIDParam) (*conversationOutput, error) {
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if len(in.ConversationID) > maxFilterStr {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "conversation_id too long (max 200 chars)")
	}
	if err := validateConversationID(in.ConversationID); err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if s.deps.GetConversation == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "conversation lookup unavailable")
	}
	detail, err := s.deps.GetConversation(ctx, ag.ID, in.ConversationID)
	if err != nil || detail == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "conversation not found")
	}
	out := &conversationOutput{Body: ConversationDetailView{
		ConversationSummaryView: conversationSummaryView(detail.ConversationSummary),
		Participants:            orEmptyStrings(detail.Participants),
		Labels:                  orEmptyStrings(detail.Labels),
		Messages:                make([]MessageSummaryView, len(detail.Messages)),
	}}
	for i, m := range detail.Messages {
		out.Body.Messages[i] = messageSummaryFromIdentity(m)
	}
	return out, nil
}
