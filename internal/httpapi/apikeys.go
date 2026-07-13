package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// APIKeyView is the non-secret metadata for an API key (list + create). The
// plaintext secret is never in this shape — it appears once, in
// CreateAPIKeyResponse.Key.
type APIKeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix" doc:"Non-secret visible prefix (e.g. e2a_acct_… / e2a_agt_…)."`
	Scope      string     `json:"scope" enum:"account,agent" doc:"account = workspace admin; agent = bound to one inbox."`
	Agent      string     `json:"agent_email,omitempty" doc:"Bound inbox email for agent-scoped keys; omitted for account scope."`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// CreateAPIKeyResponse is APIKeyView plus the one-time plaintext key — shown
// once at creation and never retrievable again.
type CreateAPIKeyResponse struct {
	APIKeyView
	Key string `json:"key" doc:"The secret key. Shown once; store it now — it cannot be retrieved later."`
}

func apiKeyView(k identity.APIKey) APIKeyView {
	agent := ""
	if k.AgentID != nil {
		agent = *k.AgentID
	}
	return APIKeyView{
		ID:         k.ID,
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		Scope:      k.Scope,
		Agent:      agent,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
	}
}

type listAPIKeysOutput struct{ Body Page[APIKeyView] }

// CreateAPIKeyRequest mirrors the legacy /api/keys body. scope defaults to
// account; scope=agent requires `agent_email` (an owned inbox email).
type CreateAPIKeyRequest struct {
	Name      string `json:"name,omitempty" doc:"Human label for the key."`
	ExpiresAt string `json:"expires_at,omitempty" format:"date-time" doc:"Optional hard expiry (RFC 3339, must be in the future). Omit for a never-expiring key."`
	Scope     string `json:"scope,omitempty" enum:"account,agent" doc:"account = workspace admin (default); agent = bound to a single inbox."`
	Agent     string `json:"agent_email,omitempty" doc:"Inbox email to bind the key to; required when scope=agent."`
}

type createAPIKeyInput struct{ Body CreateAPIKeyRequest }
type createAPIKeyOutput struct{ Body CreateAPIKeyResponse }

type deleteAPIKeyInput struct {
	ID string `path:"id"`
	DeleteConfirm
}
type deleteAPIKeyOutput struct{ Body DeleteApiKeyResult }

func (s *Server) registerAPIKeys() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listApiKeys", Method: http.MethodGet, Path: "/v1/account/api-keys",
		Summary: "List API keys", Tags: []string{"account"},
		Description: "API keys for the account (metadata only — secrets are shown once, at creation). Account scope only: an agent-scoped credential cannot manage keys.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListAPIKeys)

	huma.Register(s.API, huma.Operation{
		OperationID: "createApiKey", Method: http.MethodPost, Path: "/v1/account/api-keys",
		Summary: "Create an API key", Tags: []string{"account"},
		Description: "Mint a new API key; the plaintext key is returned once. scope=account is workspace admin (agent/domain/key management); scope=agent binds the key to one inbox so it can act only as that agent. Account scope only.",
		Security:    []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusCreated,
	}, s.handleCreateAPIKey)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteApiKey", Method: http.MethodDelete, Path: "/v1/account/api-keys/{id}",
		Summary: "Revoke an API key", Tags: []string{"account"},
		Description: "Revoke a key by id. Integrations using it stop authenticating immediately. Account scope only. Requires ?confirm=DELETE. Returns 200 with a deletion object ({deleted:true, id}).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteAPIKey)
}

// listAPIKeysInput carries the standard cursor/limit (PageParams).
type listAPIKeysInput struct {
	PageParams
}

func (s *Server) handleListAPIKeys(ctx context.Context, in *listAPIKeysInput) (*listAPIKeysOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListAPIKeys == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "API keys are not available on this deployment")
	}
	afterCreatedAt, afterID, err := s.decodeKeyset(in.Cursor)
	if err != nil {
		return nil, err
	}
	limit := effectiveLimit(in.Limit)
	// Fetch limit+1 to detect a further page.
	keys, err := s.deps.ListAPIKeys(ctx, user.ID, limit+1, afterCreatedAt, afterID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list API keys")
	}
	hasMore := len(keys) > limit
	if hasMore {
		keys = keys[:limit]
	}
	views := make([]APIKeyView, 0, len(keys))
	for _, k := range keys {
		views = append(views, apiKeyView(k))
	}
	var nextCursor string
	if hasMore {
		last := keys[len(keys)-1]
		if nextCursor, err = s.encodeKeyset(last.CreatedAt, last.ID); err != nil {
			return nil, err
		}
	}
	return &listAPIKeysOutput{Body: NewPage(views, nextCursor)}, nil
}

func (s *Server) handleCreateAPIKey(ctx context.Context, in *createAPIKeyInput) (*createAPIKeyOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.CreateScopedAPIKey == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "API keys are not available on this deployment")
	}

	// Optional expiry: RFC 3339, must be in the future. Empty = never expires.
	var expiresAt *time.Time
	if in.Body.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, in.Body.ExpiresAt)
		if perr != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_expires_at", "expires_at must be an RFC 3339 timestamp")
		}
		if !t.After(time.Now()) {
			return nil, NewError(http.StatusBadRequest, "invalid_expires_at", "expires_at must be in the future")
		}
		expiresAt = &t
	}

	scope := in.Body.Scope
	if scope == "" {
		scope = identity.ScopeAccount
	}
	if !identity.ValidScope(scope) {
		return nil, NewError(http.StatusBadRequest, "invalid_scope", "scope must be 'account' or 'agent'")
	}

	// For an agent-scoped key, resolve the named inbox to its id (ownership
	// re-checked by resolveOwnedAgent) so a wrong/foreign agent is rejected
	// rather than minting an over-broad or cross-tenant key.
	var agentID string
	if scope == identity.ScopeAgent {
		if in.Body.Agent == "" {
			return nil, NewError(http.StatusBadRequest, "invalid_request", "agent_email (inbox email) is required when scope=agent")
		}
		ag, aerr := s.resolveOwnedAgent(ctx, in.Body.Agent)
		if aerr != nil {
			return nil, aerr
		}
		agentID = ag.ID
	}

	key, err := s.deps.CreateScopedAPIKey(ctx, user.ID, in.Body.Name, scope, agentID, expiresAt)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to create API key")
	}
	return &createAPIKeyOutput{Body: CreateAPIKeyResponse{
		APIKeyView: apiKeyView(*key),
		Key:        key.PlaintextKey,
	}}, nil
}

func (s *Server) handleDeleteAPIKey(ctx context.Context, in *deleteAPIKeyInput) (*deleteAPIKeyOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.DeleteAPIKey == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "API keys are not available on this deployment")
	}
	if err := s.deps.DeleteAPIKey(ctx, in.ID, user.ID); err != nil {
		if errors.Is(err, identity.ErrAPIKeyNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "API key not found")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to revoke API key")
	}
	return &deleteAPIKeyOutput{Body: DeleteApiKeyResult{Deleted: true, ID: in.ID}}, nil
}
