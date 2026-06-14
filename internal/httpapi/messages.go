package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// MessageView is the full single-message representation. It mirrors the
// legacy GET /api/v1/agents/{email}/messages/{id} body field-for-field
// (Slice 1 is path move + conventions only — no shape change). All keys are
// emitted unconditionally to match the legacy map, including JSON null for
// absent cc/reply_to/auth_headers/raw_message. `status` carries the legacy
// delivery_status alias verbatim (the read_status rename is a later slice).
type MessageView struct {
	MessageID      string            `json:"message_id"`
	From           string            `json:"from"`
	To             []string          `json:"to"`
	CC             []string          `json:"cc"`
	ReplyTo        []string          `json:"reply_to"`
	Recipient      string            `json:"recipient"`
	Subject        string            `json:"subject"`
	ConversationID string            `json:"conversation_id"`
	Status         string            `json:"status"`
	Labels         []string          `json:"labels"`
	CreatedAt      string            `json:"created_at"`
	AuthHeaders    map[string]string `json:"auth_headers"`
	RawMessage     []byte            `json:"raw_message"`
}

func messageViewFromIdentity(m *identity.Message) MessageView {
	return MessageView{
		MessageID:      m.ID,
		From:           m.Sender,
		To:             orEmptyStrings(m.ToRecipients),
		CC:             m.CC,
		ReplyTo:        m.ReplyTo,
		Recipient:      m.Recipient,
		Subject:        m.Subject,
		ConversationID: m.ConversationID,
		Status:         m.DeliveryStatus,
		Labels:         orEmptyStrings(m.Labels),
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
		AuthHeaders:    m.AuthHeaders,
		RawMessage:     m.RawMessage,
	}
}

// MessageIDParam is the path input for single-message operations.
type MessageIDParam struct {
	Address   string `path:"address" doc:"The agent's full email address."`
	MessageID string `path:"id" doc:"The message id, e.g. msg_abc123."`
}

type messageOutput struct {
	Body MessageView
}

func (s *Server) registerMessages() {
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
