package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/gorilla/mux"
)

// --- Webhooks-as-a-resource HTTP layer (slice 2) ---
//
// The handlers here serve POST/GET/LIST/PATCH/DELETE on /api/v1/webhooks
// plus /rotate-secret, /test, /deliveries subresources. The storage
// layer in internal/identity/webhooks.go does the per-row work; this
// layer applies the public-facing validation rules from the design.

// Per-resource caps. Mirror the design's locked values.
const (
	webhookMaxAgentIDs        = 50
	webhookMaxConversationIDs = 50
	webhookMaxLabels          = 50
	webhookMaxFilterValueLen  = 200
)

// validateCreateUpdateRequest applies every validation rule the design
// mandates for both POST and PATCH (full-replace fields). Returns a
// human-readable error string (handler maps to 400) or "" if valid.
//
// The caller passes the already-resolved user (for the agent ownership
// check) and the canonical filter / events slices to validate. URL
// validation goes through the existing ValidateWebhookURL helper to
// reuse its SSRF protections.
func (a *API) validateWebhookFields(user *identity.User, url string, events []string, filters identity.WebhookFilters, description string) (msg string, status int) {
	if url != "" {
		if err := ValidateWebhookURL(url); err != nil {
			return fmt.Sprintf("invalid url: %v", err), http.StatusBadRequest
		}
		if len(url) > 2048 {
			return "url too long (max 2048 chars)", http.StatusBadRequest
		}
	}
	if len(events) == 0 {
		return "events must be a non-empty array", http.StatusBadRequest
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !webhookpub.IsValidEventType(e) {
			return fmt.Sprintf("unknown event type %q (allowed: %s)", e, strings.Join(webhookpub.AllEventTypes, ", ")), http.StatusBadRequest
		}
		if seen[e] {
			continue
		}
		seen[e] = true
	}
	if len(description) > 200 {
		return "description too long (max 200 chars)", http.StatusBadRequest
	}
	if strings.ContainsAny(description, "\r\n") {
		return "description must not contain CR or LF", http.StatusBadRequest
	}

	if len(filters.AgentIDs) > webhookMaxAgentIDs {
		return fmt.Sprintf("filters.agent_ids exceeds cap of %d", webhookMaxAgentIDs), http.StatusBadRequest
	}
	if len(filters.ConversationIDs) > webhookMaxConversationIDs {
		return fmt.Sprintf("filters.conversation_ids exceeds cap of %d", webhookMaxConversationIDs), http.StatusBadRequest
	}
	if len(filters.Labels) > webhookMaxLabels {
		return fmt.Sprintf("filters.labels exceeds cap of %d", webhookMaxLabels), http.StatusBadRequest
	}

	for _, agentEmail := range filters.AgentIDs {
		// agent_ids must reference agents the caller owns. Use the
		// existing ListAgentsByUser query rather than introducing a
		// new one; at filter-list size ≤ 50 the cost is negligible.
		// The check fails on the first unowned id with a clear
		// "cross-user agent" error message.
		if agentEmail == "" {
			return "filters.agent_ids contains empty entry", http.StatusBadRequest
		}
		if len(agentEmail) > webhookMaxFilterValueLen {
			return "filters.agent_ids entry exceeds 200 chars", http.StatusBadRequest
		}
	}
	if msg, status := a.assertAgentsOwnedByUser(user.ID, filters.AgentIDs); msg != "" {
		return msg, status
	}

	for _, c := range filters.ConversationIDs {
		if c == "" || len(c) > webhookMaxFilterValueLen {
			return "filters.conversation_ids contains empty entry or one over 200 chars", http.StatusBadRequest
		}
		// Conversation IDs are case-sensitive, charset
		// [a-zA-Z0-9_-]+.
		for _, r := range c {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_'
			if !ok {
				return fmt.Sprintf("filters.conversation_ids[%q]: invalid character", c), http.StatusBadRequest
			}
		}
	}
	for _, l := range filters.Labels {
		if l == "" || len(l) > 64 {
			return "filters.labels contains empty entry or one over 64 chars", http.StatusBadRequest
		}
		// Labels charset same as the labels feature: [a-z0-9:_-]+
		// (lowercased — the storage layer normalizes on writes but
		// since we don't normalize here, reject case-divergent
		// values rather than silently bait-and-switch).
		for _, r := range l {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') ||
				r == ':' || r == '-' || r == '_'
			if !ok {
				return fmt.Sprintf("filters.labels[%q]: invalid character (expected [a-z0-9:_-]+, lowercase)", l), http.StatusBadRequest
			}
		}
	}

	return "", 0
}

// assertAgentsOwnedByUser verifies that every email in the given slice
// is one of the user's agents. Returns "" on success. On a non-owned
// id, returns a clear error referencing the specific id so the caller
// can fix the filter without guessing.
func (a *API) assertAgentsOwnedByUser(userID string, agentEmails []string) (msg string, status int) {
	if len(agentEmails) == 0 {
		return "", 0
	}
	agents, err := a.store.ListAgentsByUser(context.Background(), userID)
	if err != nil {
		return "failed to verify agent ownership", http.StatusInternalServerError
	}
	owned := map[string]bool{}
	for _, ag := range agents {
		owned[ag.ID] = true
	}
	for _, email := range agentEmails {
		if !owned[email] {
			return fmt.Sprintf("filters.agent_ids[%q]: not owned by this user", email), http.StatusBadRequest
		}
	}
	return "", 0
}

// --- POST /api/v1/webhooks ---

// handleCreateWebhook godoc
// @Summary      Create webhook subscriber
// @Description  Creates a top-level webhook subscriber. The plaintext signing_secret is returned ONCE in this response and never echoed by any later GET. URL must be HTTPS and must resolve to a public IP. Per-user cap defaults to 50.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        body body CreateWebhookRequest true "Webhook spec"
// @Success      201  {object}  WebhookResponse
// @Failure      400  {string}  string "validation error or cap reached"
// @Failure      401  {string}  string "Unauthorized"
// @Router       /webhooks [post]
// @Security     BearerAuth
func (a *API) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	var req struct {
		URL         string                  `json:"url"`
		Events      []string                `json:"events"`
		Filters     *identity.WebhookFilters `json:"filters"`
		Description string                  `json:"description"`
	}
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var filters identity.WebhookFilters
	if req.Filters != nil {
		filters = *req.Filters
	}
	if msg, code := a.validateWebhookFields(user, req.URL, req.Events, filters, req.Description); msg != "" {
		http.Error(w, msg, code)
		return
	}

	wh, err := a.store.CreateWebhook(r.Context(), user.ID, req.URL, req.Description, req.Events, filters)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookCapReached) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[api] CreateWebhook: user=%s err=%v", user.ID, err)
		http.Error(w, "failed to create webhook", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	// Plaintext SigningSecret is included ONLY on this response.
	writeJSON(w, webhookResponseFromIdentity(wh, true))
}

// --- GET /api/v1/webhooks ---

// handleListWebhooks godoc
// @Summary      List webhook subscribers
// @Description  Returns every webhook (enabled + disabled) owned by the caller. signing_secret is omitted.
// @Tags         webhooks
// @Produce      json
// @Success      200  {object}  ListWebhooksResponse
// @Failure      401  {string}  string "Unauthorized"
// @Failure      429  {string}  string "Rate limited"
// @Router       /webhooks [get]
// @Security     BearerAuth
func (a *API) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	if ok, retryAfter := a.pollLimit.AllowWithRetryAfter(user.ID); !ok {
		writeTooManyRequests(w, retryAfter, "rate limit exceeded — max 60 requests per minute per user")
		return
	}
	hooks, err := a.store.ListWebhooksByUser(r.Context(), user.ID)
	if err != nil {
		log.Printf("[api] ListWebhooksByUser err: user=%s err=%v", user.ID, err)
		http.Error(w, "failed to list webhooks", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]interface{}, 0, len(hooks))
	for i := range hooks {
		out = append(out, webhookResponseFromIdentity(&hooks[i], false))
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{"webhooks": out})
}

// --- GET /api/v1/webhooks/{id} ---

// handleGetWebhook godoc
// @Summary      Get webhook subscriber
// @Tags         webhooks
// @Produce      json
// @Param        id path string true "Webhook ID"
// @Success      200  {object}  WebhookResponse
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Router       /webhooks/{id} [get]
// @Security     BearerAuth
func (a *API) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	if ok, retryAfter := a.pollLimit.AllowWithRetryAfter(user.ID); !ok {
		writeTooManyRequests(w, retryAfter, "rate limit exceeded — max 60 requests per minute per user")
		return
	}
	webhookID := mux.Vars(r)["id"]
	wh, err := a.store.GetWebhookByID(r.Context(), webhookID, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] GetWebhookByID err: %v", err)
		http.Error(w, "failed to fetch webhook", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, webhookResponseFromIdentity(wh, false))
}

// --- PATCH /api/v1/webhooks/{id} ---

// handleUpdateWebhook godoc
// @Summary      Update webhook subscriber
// @Description  Partial update. Fields not present are unchanged. url / events / filters are full-replace when present (no array-merge). Re-enable within 5 minutes of auto_disabled_at returns 409.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        id   path string true "Webhook ID"
// @Param        body body UpdateWebhookRequest true "Patch body"
// @Success      200  {object}  WebhookResponse
// @Failure      400  {string}  string "Validation error"
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Failure      409  {string}  string "Re-enable cooldown"
// @Router       /webhooks/{id} [patch]
// @Security     BearerAuth
func (a *API) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	webhookID := mux.Vars(r)["id"]

	var req struct {
		URL         *string                  `json:"url,omitempty"`
		Events      *[]string                `json:"events,omitempty"`
		Filters     *identity.WebhookFilters `json:"filters,omitempty"`
		Description *string                  `json:"description,omitempty"`
		Enabled     *bool                    `json:"enabled,omitempty"`
	}
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// PATCH rejects events:[] and url:"" — caller can DELETE the
	// webhook if they want it gone, but a webhook with no events or
	// no URL is in a broken state.
	if req.Events != nil && len(*req.Events) == 0 {
		http.Error(w, "events must not be empty (PATCH with events:[] rejected; DELETE the webhook to remove it)", http.StatusBadRequest)
		return
	}
	if req.URL != nil && *req.URL == "" {
		http.Error(w, "url must not be empty", http.StatusBadRequest)
		return
	}

	// Validate present fields against the create-time rules. We
	// re-fetch the current row to fill in fields not being changed
	// (validation needs the full effective state).
	current, err := a.store.GetWebhookByID(r.Context(), webhookID, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] GetWebhookByID in PATCH: %v", err)
		http.Error(w, "failed to fetch webhook", http.StatusInternalServerError)
		return
	}
	effectiveURL := current.URL
	if req.URL != nil {
		effectiveURL = *req.URL
	}
	effectiveEvents := current.Events
	if req.Events != nil {
		effectiveEvents = *req.Events
	}
	effectiveFilters := current.Filters
	if req.Filters != nil {
		effectiveFilters = *req.Filters
	}
	effectiveDesc := current.Description
	if req.Description != nil {
		effectiveDesc = *req.Description
	}
	if msg, code := a.validateWebhookFields(user, effectiveURL, effectiveEvents, effectiveFilters, effectiveDesc); msg != "" {
		http.Error(w, msg, code)
		return
	}

	wh, err := a.store.UpdateWebhook(r.Context(), webhookID, user.ID, identity.WebhookUpdate{
		URL:         req.URL,
		Events:      req.Events,
		Filters:     req.Filters,
		Description: req.Description,
		Enabled:     req.Enabled,
	})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrWebhookNotFound):
			http.Error(w, "webhook not found", http.StatusNotFound)
		case errors.Is(err, identity.ErrWebhookCooldown):
			// 409 Conflict matches the design's locked decision #10.
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			log.Printf("[api] UpdateWebhook err: %v", err)
			http.Error(w, "failed to update webhook", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, webhookResponseFromIdentity(wh, false))
}

// --- DELETE /api/v1/webhooks/{id} ---

// handleDeleteWebhook godoc
// @Summary      Delete webhook subscriber
// @Description  Removes the webhook and cascades pending deliveries.
// @Tags         webhooks
// @Param        id path string true "Webhook ID"
// @Success      204  "No content"
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Router       /webhooks/{id} [delete]
// @Security     BearerAuth
func (a *API) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	webhookID := mux.Vars(r)["id"]
	if err := a.store.DeleteWebhook(r.Context(), webhookID, user.ID); err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] DeleteWebhook err: %v", err)
		http.Error(w, "failed to delete webhook", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /api/v1/webhooks/{id}/rotate-secret ---

// handleRotateWebhookSecret godoc
// @Summary      Rotate webhook signing secret
// @Description  Generates a new signing secret and moves the current one into a 24h grace window during which the worker dual-signs each delivery.
// @Tags         webhooks
// @Produce      json
// @Param        id path string true "Webhook ID"
// @Success      200  {object}  RotateWebhookSecretResponse
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Router       /webhooks/{id}/rotate-secret [post]
// @Security     BearerAuth
func (a *API) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	webhookID := mux.Vars(r)["id"]
	newSecret, prevExpiresAt, err := a.store.RotateSecret(r.Context(), webhookID, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] RotateSecret err: %v", err)
		http.Error(w, "failed to rotate webhook secret", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"signing_secret":             newSecret,
		"previous_secret_expires_at": prevExpiresAt.UTC().Format(time.RFC3339),
	})
}

// --- POST /api/v1/webhooks/{id}/test ---

// handleTestWebhook godoc
// @Summary      Fire a synthetic webhook event for development
// @Description  Schedules a one-off delivery to the webhook with a synthetic envelope, bypassing filter matching. Returns the delivery_id so the caller can correlate it in /deliveries. Returns 409 when the webhook is disabled.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        id   path string true "Webhook ID"
// @Param        body body TestWebhookRequest true "Synthetic event"
// @Success      200  {object}  TestWebhookResponse
// @Failure      400  {string}  string "Bad request"
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Failure      409  {string}  string "Webhook disabled"
// @Router       /webhooks/{id}/test [post]
// @Security     BearerAuth
func (a *API) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	if a.subscriberStore == nil {
		http.Error(w, "test endpoint not configured", http.StatusNotFound)
		return
	}
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	webhookID := mux.Vars(r)["id"]
	wh, err := a.store.GetWebhookByID(r.Context(), webhookID, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] GetWebhookByID for /test: %v", err)
		http.Error(w, "failed to fetch webhook", http.StatusInternalServerError)
		return
	}
	if !wh.Enabled {
		// Decision: /test on a disabled webhook returns 409.
		http.Error(w, "webhook is disabled", http.StatusConflict)
		return
	}

	var req struct {
		Event string                 `json:"event"`
		Data  map[string]interface{} `json:"data"`
	}
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Event == "" {
		req.Event = webhookpub.EventEmailReceived
	}
	if !webhookpub.IsValidEventType(req.Event) {
		http.Error(w, fmt.Sprintf("unknown event type %q", req.Event), http.StatusBadRequest)
		return
	}
	// The /test endpoint inserts a delivery row directly — it bypasses
	// the publisher's filter-matching logic because the test is
	// already targeted at a specific webhook id.
	data := req.Data
	if data == nil {
		data = map[string]interface{}{"test": true}
	}
	event := webhookpub.NewEvent(req.Event, user.ID, data)
	envelopeJSON, err := json.Marshal(event.AsEnvelope())
	if err != nil {
		log.Printf("[api] /test marshal envelope: %v", err)
		http.Error(w, "failed to marshal test event", http.StatusInternalServerError)
		return
	}

	deliveryID, err := a.subscriberStore.InsertPendingForTest(r.Context(), wh.ID, req.Event, envelopeJSON)
	if err != nil {
		log.Printf("[api] /test insert delivery: %v", err)
		http.Error(w, "failed to schedule test delivery", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{"delivery_id": deliveryID})
}

// --- GET /api/v1/webhooks/{id}/deliveries ---

// handleListWebhookDeliveries godoc
// @Summary      List recent webhook deliveries
// @Description  Returns the most recent delivery attempts for the webhook (capped at 100). Optional status query restricts to pending|delivered|failed.
// @Tags         webhooks
// @Produce      json
// @Param        id     path  string true  "Webhook ID"
// @Param        limit  query int    false "Page size (default 20, max 100)"
// @Param        status query string false "Filter by status: pending|delivered|failed"
// @Success      200  {object}  ListWebhookDeliveriesResponse
// @Failure      400  {string}  string "Bad query"
// @Failure      401  {string}  string "Unauthorized"
// @Failure      404  {string}  string "Not found"
// @Failure      429  {string}  string "Rate limited"
// @Router       /webhooks/{id}/deliveries [get]
// @Security     BearerAuth
func (a *API) handleListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if a.subscriberStore == nil {
		http.Error(w, "deliveries endpoint not configured", http.StatusNotFound)
		return
	}
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	if ok, retryAfter := a.pollLimit.AllowWithRetryAfter(user.ID); !ok {
		writeTooManyRequests(w, retryAfter, "rate limit exceeded — max 60 requests per minute per user")
		return
	}
	webhookID := mux.Vars(r)["id"]
	// Confirm ownership before reading delivery rows.
	if _, err := a.store.GetWebhookByID(r.Context(), webhookID, user.ID); err != nil {
		if errors.Is(err, identity.ErrWebhookNotFound) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] GetWebhookByID for /deliveries: %v", err)
		http.Error(w, "failed to fetch webhook", http.StatusInternalServerError)
		return
	}

	limit := 20
	if s := strings.TrimSpace(r.URL.Query().Get("limit")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			http.Error(w, "limit must be an integer in [1, 100]", http.StatusBadRequest)
			return
		}
		limit = n
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "pending" && status != "delivered" && status != "failed" {
		http.Error(w, "status must be one of pending|delivered|failed", http.StatusBadRequest)
		return
	}

	rows, err := a.subscriberStore.ListDeliveriesByWebhook(r.Context(), webhookID, status, limit)
	if err != nil {
		log.Printf("[api] ListDeliveriesByWebhook err: %v", err)
		http.Error(w, "failed to list deliveries", http.StatusInternalServerError)
		return
	}

	out := make([]map[string]interface{}, 0, len(rows))
	for _, d := range rows {
		row := map[string]interface{}{
			"id":          d.ID,
			"event_type":  d.EventType,
			"status":      d.Status,
			"attempts":    d.Attempts,
			"next_retry_at": d.NextRetryAt.UTC().Format(time.RFC3339),
			"created_at":  d.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d.LastError != "" {
			row["last_error"] = d.LastError
		}
		if d.LastStatusCode != nil {
			row["last_status_code"] = *d.LastStatusCode
		}
		if d.LastAttemptAt != nil {
			row["last_attempt_at"] = d.LastAttemptAt.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{"deliveries": out})
}

// --- helpers ---

// webhookResponseFromIdentity builds the wire response from a stored
// row. When includeSecret is true, the signing_secret plaintext is
// included (POST + rotate). Every other endpoint omits it.
func webhookResponseFromIdentity(wh *identity.Webhook, includeSecret bool) map[string]interface{} {
	out := map[string]interface{}{
		"id":          wh.ID,
		"url":         wh.URL,
		"description": wh.Description,
		"events":      wh.Events,
		"filters":     wh.Filters,
		"enabled":     wh.Enabled,
		"created_at":  wh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if includeSecret {
		out["signing_secret"] = wh.SigningSecret
	}
	if wh.AutoDisabledAt != nil {
		out["auto_disabled_at"] = wh.AutoDisabledAt.UTC().Format(time.RFC3339)
	}
	if wh.LastDeliveredAt != nil {
		out["last_delivered_at"] = wh.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	return out
}
