package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/danielgtaylor/huma/v2"
)

const (
	webhookMaxAgentIDs        = 50
	webhookMaxConversationIDs = 50
	webhookMaxLabels          = 50
	webhookMaxFilterValueLen  = 200
	webhookMaxURLLen          = 2048
	webhookMaxDescriptionLen  = 200
)

// WebhookFiltersView mirrors identity.WebhookFilters.
type WebhookFiltersView struct {
	AgentIDs        []string `json:"agent_ids,omitempty"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
	Labels          []string `json:"labels,omitempty"`
}

// WebhookView mirrors the legacy webhookResponseFromIdentity shape.
// SigningSecret is populated ONLY on create + rotate; every other path
// omits it so a stolen API key can't exfiltrate secrets via list/get.
type WebhookView struct {
	ID                      string             `json:"id"`
	URL                     string             `json:"url"`
	Description             string             `json:"description"`
	Events                  []string           `json:"events"`
	Filters                 WebhookFiltersView `json:"filters"`
	SigningSecret           string             `json:"signing_secret,omitempty"`
	PreviousSecretExpiresAt string             `json:"previous_secret_expires_at,omitempty"`
	Enabled                 bool               `json:"enabled"`
	AutoDisabledAt          string             `json:"auto_disabled_at,omitempty"`
	CreatedAt               string             `json:"created_at"`
	LastDeliveredAt         string             `json:"last_delivered_at,omitempty"`
}

func webhookView(wh *identity.Webhook, includeSecret bool) WebhookView {
	v := WebhookView{
		ID:          wh.ID,
		URL:         wh.URL,
		Description: wh.Description,
		Events:      wh.Events,
		Filters: WebhookFiltersView{
			AgentIDs:        wh.Filters.AgentIDs,
			ConversationIDs: wh.Filters.ConversationIDs,
			Labels:          wh.Filters.Labels,
		},
		Enabled:   wh.Enabled,
		CreatedAt: wh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if includeSecret {
		v.SigningSecret = wh.SigningSecret
	}
	if wh.AutoDisabledAt != nil {
		v.AutoDisabledAt = wh.AutoDisabledAt.UTC().Format(time.RFC3339)
	}
	if wh.LastDeliveredAt != nil {
		v.LastDeliveredAt = wh.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	return v
}

// validateWebhookFields replicates the legacy validation. The two
// security-critical checks are reused from their canonical homes (SSRF via
// agent.ValidateWebhookURL, event allowlist via webhookpub.IsValidEventType)
// so they can't drift; the charset/length/ownership checks are explicit.
func (s *Server) validateWebhookFields(ctx context.Context, userID, url string, events []string, f WebhookFiltersView, description string) *ErrorEnvelope {
	if url != "" {
		if err := agent.ValidateWebhookURL(url); err != nil {
			return NewError(http.StatusBadRequest, "invalid_webhook_url", "invalid url: "+err.Error())
		}
		if len(url) > webhookMaxURLLen {
			return NewError(http.StatusBadRequest, "invalid_request", "url too long (max 2048 chars)")
		}
	}
	if len(events) == 0 {
		return NewError(http.StatusBadRequest, "invalid_request", "events must be a non-empty array")
	}
	for _, e := range events {
		if !webhookpub.IsValidEventType(e) {
			return NewError(http.StatusBadRequest, "invalid_event_type",
				fmt.Sprintf("unknown event type %q (allowed: %s)", e, strings.Join(webhookpub.AllEventTypes, ", ")))
		}
	}
	if len(description) > webhookMaxDescriptionLen {
		return NewError(http.StatusBadRequest, "invalid_request", "description too long (max 200 chars)")
	}
	if strings.ContainsAny(description, "\r\n") {
		return NewError(http.StatusBadRequest, "invalid_request", "description must not contain CR or LF")
	}
	if len(f.AgentIDs) > webhookMaxAgentIDs {
		return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.agent_ids exceeds cap of %d", webhookMaxAgentIDs))
	}
	if len(f.ConversationIDs) > webhookMaxConversationIDs {
		return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.conversation_ids exceeds cap of %d", webhookMaxConversationIDs))
	}
	if len(f.Labels) > webhookMaxLabels {
		return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.labels exceeds cap of %d", webhookMaxLabels))
	}
	for _, a := range f.AgentIDs {
		if a == "" || len(a) > webhookMaxFilterValueLen {
			return NewError(http.StatusBadRequest, "invalid_request", "filters.agent_ids contains empty entry or one over 200 chars")
		}
	}
	// agent_ids must reference agents the caller owns.
	if err := s.assertAgentsOwned(ctx, userID, f.AgentIDs); err != nil {
		return err
	}
	for _, c := range f.ConversationIDs {
		if c == "" || len(c) > webhookMaxFilterValueLen {
			return NewError(http.StatusBadRequest, "invalid_request", "filters.conversation_ids contains empty entry or one over 200 chars")
		}
		for _, r := range c {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.conversation_ids[%q]: invalid character", c))
			}
		}
	}
	for _, l := range f.Labels {
		if l == "" || len(l) > 64 {
			return NewError(http.StatusBadRequest, "invalid_request", "filters.labels contains empty entry or one over 64 chars")
		}
		for _, r := range l {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == '-' || r == '_') {
				return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.labels[%q]: invalid character (expected [a-z0-9:_-]+, lowercase)", l))
			}
		}
	}
	return nil
}

// assertAgentsOwned verifies every referenced agent_id is owned by the user,
// mirroring the legacy assertAgentsOwnedByUser (uses ListAgents at filter-list
// scale ≤50).
func (s *Server) assertAgentsOwned(ctx context.Context, userID string, agentIDs []string) *ErrorEnvelope {
	if len(agentIDs) == 0 {
		return nil
	}
	agents, err := s.deps.ListAgents(ctx, userID)
	if err != nil {
		return NewError(http.StatusInternalServerError, "internal_error", "failed to validate agent filters")
	}
	owned := make(map[string]struct{}, len(agents))
	for i := range agents {
		owned[agents[i].ID] = struct{}{}
	}
	for _, id := range agentIDs {
		if _, ok := owned[identity.NormalizeEmail(id)]; !ok {
			return NewError(http.StatusBadRequest, "invalid_request", fmt.Sprintf("filters.agent_ids references an agent you don't own: %q", id))
		}
	}
	return nil
}

type webhookOutput struct{ Body WebhookView }
type webhookCreateOutput struct{ Body WebhookView }
type listWebhooksOutput struct {
	Body struct {
		Webhooks []WebhookView `json:"webhooks"`
	}
}

// CreateWebhookRequest mirrors the legacy body.
type CreateWebhookRequest struct {
	URL         string              `json:"url,omitempty"`
	Events      []string            `json:"events,omitempty"`
	Filters     *WebhookFiltersView `json:"filters,omitempty"`
	Description string              `json:"description,omitempty"`
}
type createWebhookInput struct{ Body CreateWebhookRequest }

// WebhookIDParam is the path input for single-webhook ops.
type WebhookIDParam struct {
	ID string `path:"id"`
}

func (s *Server) registerWebhooks() {
	huma.Register(s.API, huma.Operation{
		OperationID: "createWebhook", Method: http.MethodPost, Path: "/v1/webhooks",
		Summary: "Create a webhook", Tags: []string{"webhooks"},
		Security: []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusCreated,
	}, s.handleCreateWebhook)

	huma.Register(s.API, huma.Operation{
		OperationID: "listWebhooks", Method: http.MethodGet, Path: "/v1/webhooks",
		Summary: "List webhooks", Tags: []string{"webhooks"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleListWebhooks)

	huma.Register(s.API, huma.Operation{
		OperationID: "getWebhook", Method: http.MethodGet, Path: "/v1/webhooks/{id}",
		Summary: "Get a webhook", Tags: []string{"webhooks"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleGetWebhook)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteWebhook", Method: http.MethodDelete, Path: "/v1/webhooks/{id}",
		Summary: "Delete a webhook", Tags: []string{"webhooks"},
		Security: []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusNoContent,
	}, s.handleDeleteWebhook)

	huma.Register(s.API, huma.Operation{
		OperationID: "updateWebhook", Method: http.MethodPatch, Path: "/v1/webhooks/{id}",
		Summary: "Update a webhook", Tags: []string{"webhooks"},
		Description: "Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleUpdateWebhook)

	huma.Register(s.API, huma.Operation{
		OperationID: "rotateWebhookSecret", Method: http.MethodPost, Path: "/v1/webhooks/{id}/rotate-secret",
		Summary: "Rotate a webhook signing secret", Tags: []string{"webhooks"},
		Description: "Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleRotateWebhookSecret)

	huma.Register(s.API, huma.Operation{
		OperationID: "testWebhook", Method: http.MethodPost, Path: "/v1/webhooks/{id}/test",
		Summary: "Fire a synthetic event", Tags: []string{"webhooks"},
		Description: "Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleTestWebhook)

	huma.Register(s.API, huma.Operation{
		OperationID: "listWebhookDeliveries", Method: http.MethodGet, Path: "/v1/webhooks/{id}/deliveries",
		Summary: "List webhook deliveries", Tags: []string{"webhooks"},
		Description: "The per-webhook delivery log (read-only debug view).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListWebhookDeliveries)
}

// TestWebhookRequest mirrors the legacy body.
type TestWebhookRequest struct {
	Event string         `json:"event,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}
type testWebhookInput struct {
	ID   string `path:"id"`
	Body TestWebhookRequest
}
type testWebhookOutput struct {
	Body struct {
		DeliveryID string `json:"delivery_id"`
	}
}

func (s *Server) handleTestWebhook(ctx context.Context, in *testWebhookInput) (*testWebhookOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.TestWebhookInsert == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "test endpoint not configured")
	}
	wh, err := s.deps.GetWebhook(ctx, in.ID, user.ID)
	if err != nil || wh == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
	}
	if !wh.Enabled {
		return nil, NewError(http.StatusConflict, "webhook_disabled", "webhook is disabled")
	}
	event := in.Body.Event
	if event == "" {
		event = webhookpub.EventEmailReceived
	}
	if !webhookpub.IsValidEventType(event) {
		return nil, NewError(http.StatusBadRequest, "invalid_event_type", fmt.Sprintf("unknown event type %q", event))
	}
	data := in.Body.Data
	if data == nil {
		data = map[string]any{"test": true}
	}
	envelope, err := json.Marshal(webhookpub.NewEvent(event, user.ID, data).AsEnvelope())
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to marshal test event")
	}
	deliveryID, err := s.deps.TestWebhookInsert(ctx, wh.ID, event, envelope)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to schedule test delivery")
	}
	out := &testWebhookOutput{}
	out.Body.DeliveryID = deliveryID
	return out, nil
}

// WebhookDeliveryView mirrors the legacy per-delivery wire shape.
type WebhookDeliveryView struct {
	ID             string `json:"id"`
	EventType      string `json:"event_type" enum:"email.received,email.sent,email.pending_approval,email.approved,email.rejected,domain.sending_verified,domain.sending_failed,email.delivered,email.bounced,email.complained,domain.suppression_added,email.flagged"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	LastError      string `json:"last_error,omitempty"`
	LastStatusCode *int   `json:"last_status_code,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	NextRetryAt    string `json:"next_retry_at"`
	CreatedAt      string `json:"created_at"`
}

type ListDeliveriesInput struct {
	ID     string `path:"id"`
	Status string `query:"status" enum:"pending,delivered,failed"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type listDeliveriesOutput struct {
	Body Page[WebhookDeliveryView]
}

func (s *Server) handleListWebhookDeliveries(ctx context.Context, in *ListDeliveriesInput) (*listDeliveriesOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListDeliveries == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "deliveries endpoint not configured")
	}
	// Ownership: deliveries are scoped to a webhook the caller owns.
	if wh, err := s.deps.GetWebhook(ctx, in.ID, user.ID); err != nil || wh == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.deps.ListDeliveries(ctx, in.ID, in.Status, limit)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list deliveries")
	}
	items := make([]WebhookDeliveryView, 0, len(rows))
	for _, d := range rows {
		v := WebhookDeliveryView{
			ID: d.ID, EventType: d.EventType, Status: d.Status, Attempts: d.Attempts,
			LastError: d.LastError, LastStatusCode: d.LastStatusCode,
			NextRetryAt: d.NextRetryAt.UTC().Format(time.RFC3339),
			CreatedAt:   d.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d.LastAttemptAt != nil {
			v.LastAttemptAt = d.LastAttemptAt.UTC().Format(time.RFC3339)
		}
		items = append(items, v)
	}
	// No cursor continuation in the store (limit only) — next_cursor null.
	return &listDeliveriesOutput{Body: NewPage(items, "")}, nil
}

// UpdateWebhookRequest mirrors the legacy PATCH body — pointer fields so
// absent != zero; url/events/filters are full-replace when present.
type UpdateWebhookRequest struct {
	URL         *string             `json:"url,omitempty"`
	Events      *[]string           `json:"events,omitempty"`
	Filters     *WebhookFiltersView `json:"filters,omitempty"`
	Description *string             `json:"description,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
}
type updateWebhookInput struct {
	ID   string `path:"id"`
	Body UpdateWebhookRequest
}

func (s *Server) handleUpdateWebhook(ctx context.Context, in *updateWebhookInput) (*webhookOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	req := in.Body
	// A webhook with no events or no URL is a broken state — reject the
	// clearing patch (DELETE the webhook to remove it).
	if req.Events != nil && len(*req.Events) == 0 {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "events must not be empty (DELETE the webhook to remove it)")
	}
	if req.URL != nil && *req.URL == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "url must not be empty")
	}
	// Validate the effective post-patch state against the create-time rules.
	current, err := s.deps.GetWebhook(ctx, in.ID, user.ID)
	if err != nil || current == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
	}
	effURL := current.URL
	if req.URL != nil {
		effURL = *req.URL
	}
	effEvents := current.Events
	if req.Events != nil {
		effEvents = *req.Events
	}
	effFilters := WebhookFiltersView{AgentIDs: current.Filters.AgentIDs, ConversationIDs: current.Filters.ConversationIDs, Labels: current.Filters.Labels}
	if req.Filters != nil {
		effFilters = *req.Filters
	}
	effDesc := current.Description
	if req.Description != nil {
		effDesc = *req.Description
	}
	if env := s.validateWebhookFields(ctx, user.ID, effURL, effEvents, effFilters, effDesc); env != nil {
		return nil, env
	}
	var idFilters *identity.WebhookFilters
	if req.Filters != nil {
		idFilters = &identity.WebhookFilters{AgentIDs: req.Filters.AgentIDs, ConversationIDs: req.Filters.ConversationIDs, Labels: req.Filters.Labels}
	}
	wh, err := s.deps.UpdateWebhook(ctx, in.ID, user.ID, identity.WebhookUpdate{
		URL: req.URL, Events: req.Events, Filters: idFilters, Description: req.Description, Enabled: req.Enabled,
	})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrWebhookNotFound):
			return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
		case errors.Is(err, identity.ErrWebhookCooldown):
			return nil, NewError(http.StatusConflict, "webhook_cooldown", err.Error())
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to update webhook")
		}
	}
	return &webhookOutput{Body: webhookView(wh, false)}, nil
}

type rotateSecretOutput struct {
	Body struct {
		SigningSecret           string `json:"signing_secret"`
		PreviousSecretExpiresAt string `json:"previous_secret_expires_at"`
	}
}

func (s *Server) handleRotateWebhookSecret(ctx context.Context, in *WebhookIDParam) (*rotateSecretOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	secret, prevExpires, err := s.deps.RotateSecret(ctx, in.ID, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to rotate webhook secret")
	}
	out := &rotateSecretOutput{}
	out.Body.SigningSecret = secret
	out.Body.PreviousSecretExpiresAt = prevExpires.UTC().Format(time.RFC3339)
	return out, nil
}

func (s *Server) handleCreateWebhook(ctx context.Context, in *createWebhookInput) (*webhookCreateOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	var filters WebhookFiltersView
	if in.Body.Filters != nil {
		filters = *in.Body.Filters
	}
	if env := s.validateWebhookFields(ctx, user.ID, in.Body.URL, in.Body.Events, filters, in.Body.Description); env != nil {
		return nil, env
	}
	wh, err := s.deps.CreateWebhook(ctx, user.ID, in.Body.URL, in.Body.Description, in.Body.Events, identity.WebhookFilters{
		AgentIDs:        filters.AgentIDs,
		ConversationIDs: filters.ConversationIDs,
		Labels:          filters.Labels,
	})
	if err != nil {
		if errors.Is(err, identity.ErrWebhookCapReached) {
			return nil, NewError(http.StatusBadRequest, "webhook_cap_reached", err.Error())
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to create webhook")
	}
	return &webhookCreateOutput{Body: webhookView(wh, true)}, nil
}

func (s *Server) handleListWebhooks(ctx context.Context, _ *struct{}) (*listWebhooksOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	hooks, err := s.deps.ListWebhooks(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list webhooks")
	}
	out := &listWebhooksOutput{}
	out.Body.Webhooks = make([]WebhookView, 0, len(hooks))
	for i := range hooks {
		out.Body.Webhooks = append(out.Body.Webhooks, webhookView(&hooks[i], false))
	}
	return out, nil
}

func (s *Server) handleGetWebhook(ctx context.Context, in *WebhookIDParam) (*webhookOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	wh, err := s.deps.GetWebhook(ctx, in.ID, user.ID)
	if err != nil || wh == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
	}
	return &webhookOutput{Body: webhookView(wh, false)}, nil
}

type deleteWebhookOutput struct{}

func (s *Server) handleDeleteWebhook(ctx context.Context, in *WebhookIDParam) (*deleteWebhookOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.deps.DeleteWebhook(ctx, in.ID, user.ID); err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "webhook not found")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete webhook")
	}
	return &deleteWebhookOutput{}, nil
}
