package httpapi

import (
	"context"
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
}

func (s *Server) handleCreateWebhook(ctx context.Context, in *createWebhookInput) (*webhookCreateOutput, error) {
	user, err := s.requireUser(ctx)
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
	user, err := s.requireUser(ctx)
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
	user, err := s.requireUser(ctx)
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
	user, err := s.requireUser(ctx)
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
