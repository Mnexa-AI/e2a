package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/danielgtaylor/huma/v2"
)

// EventQuery is the resolved filter + cursor position passed to the events
// query closure.
type EventQuery struct {
	UserID                        string
	Type, AgentID, ConversationID string
	MessageID                     string
	Since, Until                  *time.Time
	CursorCreatedAt               time.Time
	CursorID                      string
	Limit                         int
}

// eventsCursor is the opaque continuation: the last row's (created_at, id).
type eventsCursor struct {
	C time.Time `json:"c"`
	I string    `json:"i"`
}

// ListEventsInput — filters + the standardized cursor/limit (replacing the
// legacy page_size/token).
type ListEventsInput struct {
	Type           string `query:"type"`
	AgentID        string `query:"agent_id"`
	ConversationID string `query:"conversation_id"`
	MessageID      string `query:"message_id"`
	Since          string `query:"since" doc:"RFC3339."`
	Until          string `query:"until" doc:"RFC3339."`
	Cursor         string `query:"cursor"`
	Limit          int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type listEventsOutput struct {
	Body Page[agent.EventJSON]
}

type eventOutput struct {
	Body agent.EventJSON
}

func (s *Server) registerEvents() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listEvents", Method: http.MethodGet, Path: "/v1/events",
		Summary: "List events", Tags: []string{"events"},
		Description: "The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListEvents)

	huma.Register(s.API, huma.Operation{
		OperationID: "getEvent", Method: http.MethodGet, Path: "/v1/events/{id}",
		Summary: "Get an event", Tags: []string{"events"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleGetEvent)

	huma.Register(s.API, huma.Operation{
		OperationID: "redeliverEvent", Method: http.MethodPost, Path: "/v1/events/{id}/redeliver",
		Summary: "Redeliver an event", Tags: []string{"events"},
		Description: "Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleRedeliverEvent)
}

// RedeliverView mirrors the legacy redeliverResponse.
type RedeliverView struct {
	DeliveryID string              `json:"delivery_id,omitempty"`
	EventID    string              `json:"event_id"`
	WebhookID  string              `json:"webhook_id,omitempty"`
	Status     string              `json:"status"`
	Deliveries []RedeliverDelivery `json:"deliveries,omitempty"`
}

type RedeliverDelivery struct {
	WebhookID  string `json:"webhook_id"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type RedeliverEventInput struct {
	ID   string `path:"id"`
	Body struct {
		WebhookID string `json:"webhook_id,omitempty"`
	}
}

type redeliverOutput struct {
	Body RedeliverView
}

func (s *Server) handleRedeliverEvent(ctx context.Context, in *RedeliverEventInput) (*redeliverOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.LoadReplayEvent == nil || s.deps.InsertReplayDelivery == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "events API not configured")
	}
	if in.ID == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "missing event id")
	}
	webhookID := in.Body.WebhookID

	// Auto-idempotency keyed on (event, webhook): the same redeliver within
	// the window replays the cached schedule rather than double-enqueueing.
	key := "replay:" + in.ID + ":" + webhookID
	_, body, err := runIdempotent(s, ctx, user.ID, key, "/v1/events/redeliver", nil, func() (int, RedeliverView, error) {
		row, lerr := s.deps.LoadReplayEvent(ctx, user.ID, in.ID)
		if lerr != nil {
			switch {
			case errors.Is(lerr, agent.ErrEventNotFound):
				return 0, RedeliverView{}, NewError(http.StatusNotFound, "not_found", "event not found")
			case errors.Is(lerr, agent.ErrEventExpired):
				return 0, RedeliverView{}, NewError(http.StatusGone, "gone", "event expired (past 30-day retention)")
			default:
				return 0, RedeliverView{}, NewError(http.StatusInternalServerError, "internal_error", "failed to load event")
			}
		}
		if webhookID != "" {
			if !containsStr(row.MatchedWebhookIDs, webhookID) {
				return 0, RedeliverView{}, NewError(http.StatusConflict, "conflict", "webhook was not among the originally-matched subscribers")
			}
			dl, derr := s.deps.InsertReplayDelivery(ctx, in.ID, webhookID, row.EventType, row.MessageID, row.Envelope)
			if derr != nil {
				return 0, RedeliverView{}, NewError(http.StatusInternalServerError, "internal_error", "failed to schedule replay")
			}
			return http.StatusOK, RedeliverView{DeliveryID: dl, EventID: in.ID, WebhookID: webhookID, Status: "pending"}, nil
		}
		// Bulk fan-out to every originally-matched subscriber.
		deliveries := make([]RedeliverDelivery, 0, len(row.MatchedWebhookIDs))
		for _, whID := range row.MatchedWebhookIDs {
			dl, derr := s.deps.InsertReplayDelivery(ctx, in.ID, whID, row.EventType, row.MessageID, row.Envelope)
			if derr != nil {
				deliveries = append(deliveries, RedeliverDelivery{WebhookID: whID, Status: "skipped", Reason: "failed to schedule"})
				continue
			}
			deliveries = append(deliveries, RedeliverDelivery{WebhookID: whID, DeliveryID: dl, Status: "pending"})
		}
		return http.StatusOK, RedeliverView{EventID: in.ID, Status: "scheduled", Deliveries: deliveries}, nil
	})
	if err != nil {
		return nil, err
	}
	return &redeliverOutput{Body: body}, nil
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func (s *Server) handleListEvents(ctx context.Context, in *ListEventsInput) (*listEventsOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListEvents == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "events API not configured")
	}
	since, err := parseRFC3339FilterPtr(in.Since, "since")
	if err != nil {
		return nil, err
	}
	until, err := parseRFC3339FilterPtr(in.Until, "until")
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	var cur eventsCursor
	if in.Cursor != "" {
		if err := DecodeCursor(in.Cursor, &cur); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_cursor", "invalid pagination cursor")
		}
	}
	events, err := s.deps.ListEvents(ctx, EventQuery{
		UserID: user.ID, Type: in.Type, AgentID: in.AgentID, ConversationID: in.ConversationID,
		MessageID: in.MessageID, Since: since, Until: until,
		CursorCreatedAt: cur.C, CursorID: cur.I, Limit: limit,
	})
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list events")
	}
	var nextCursor string
	if len(events) == limit {
		last := events[len(events)-1]
		nextCursor, err = EncodeCursor(eventsCursor{C: last.CreatedAt, I: last.ID})
		if err != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to build pagination cursor")
		}
	}
	return &listEventsOutput{Body: NewPage(events, nextCursor)}, nil
}

func (s *Server) handleGetEvent(ctx context.Context, in *struct {
	ID string `path:"id"`
}) (*eventOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.GetEvent2 == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "events API not configured")
	}
	if in.ID == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "missing event id")
	}
	ej, err := s.deps.GetEvent2(ctx, user.ID, in.ID)
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrEventNotFound):
			return nil, NewError(http.StatusNotFound, "not_found", "event not found")
		case errors.Is(err, agent.ErrEventExpired):
			return nil, NewError(http.StatusGone, "gone", "event expired (past 30-day retention)")
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to fetch event")
		}
	}
	return &eventOutput{Body: *ej}, nil
}

// parseRFC3339FilterPtr is the *time.Time variant of parseRFC3339Filter.
func parseRFC3339FilterPtr(raw, name string) (*time.Time, error) {
	t, err := parseRFC3339Filter(raw, name)
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}
