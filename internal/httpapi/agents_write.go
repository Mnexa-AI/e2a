package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/danielgtaylor/huma/v2"
)

// CreateAgentRequest is the /v1 agent-create body. The legacy agent_mode and
// webhook_url fields were dropped (migration 029): push is delivered solely
// via the /v1/webhooks subscriber resource and WebSocket is open to all
// agents, so per-agent mode/webhook no longer exist.
// Fields are schema-optional (omitempty) so validation is handler-owned and
// uniform — the legacy 400 business-rule messages, not Huma's 422 (email
// itself can't be schema-required since the slug path derives it).
type CreateAgentRequest struct {
	Email string `json:"email,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Name  string `json:"name,omitempty"`
}

// CreateAgentResponse mirrors the legacy RegisterAgentResponse.
type CreateAgentResponse struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Email  string `json:"email"`
}

type createAgentInput struct {
	Body CreateAgentRequest
}

type createAgentOutput struct {
	Body CreateAgentResponse
}

// slugPattern / reservedSlugs replicate the legacy validateSlug rule (slug
// registration is a legacy concept being dropped; the values move home or
// disappear at the 1Z cutover).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

var reservedSlugs = map[string]bool{
	"admin": true, "postmaster": true, "abuse": true, "noreply": true,
	"no-reply": true, "mailer-daemon": true, "info": true, "help": true,
	"demo": true, "test": true, "www": true, "mail": true, "agent": true,
	"api": true, "system": true, "root": true,
}

func validateSlug(slug string) error {
	if len(slug) < 2 || len(slug) > 40 {
		return errSlug("slug must be 2–40 characters")
	}
	if !slugPattern.MatchString(slug) {
		return errSlug("slug must be lowercase alphanumeric with hyphens, no leading/trailing hyphens")
	}
	if reservedSlugs[slug] {
		return errSlug("slug is reserved")
	}
	return nil
}

func errSlug(msg string) error { return &slugError{msg} }

type slugError struct{ msg string }

func (e *slugError) Error() string { return e.msg }

func (s *Server) registerAgentWrites() {
	huma.Register(s.API, huma.Operation{
		OperationID:   "createAgent",
		Method:        http.MethodPost,
		Path:          "/v1/agents",
		Summary:       "Create an agent",
		Description:   "Register an agent on a verified domain the caller owns (or, when slug registration is enabled, on the shared domain).",
		Tags:          []string{"agents"},
		Security:      []map[string][]string{{"bearer": {}}},
		DefaultStatus: http.StatusCreated,
	}, s.handleCreateAgent)

	huma.Register(s.API, huma.Operation{
		OperationID: "updateAgent",
		Method:      http.MethodPatch,
		Path:        "/v1/agents/{address}",
		Summary:     "Update an agent",
		Description: "Patch an agent's HITL settings. Returns the post-update agent.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleUpdateAgent)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteAgent",
		Method:      http.MethodDelete,
		Path:        "/v1/agents/{address}",
		Summary:     "Delete an agent",
		Description: "Delete an agent the caller owns.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteAgent)
}

// UpdateAgentRequest is the /v1 agent PATCH body — pointer fields so
// absent != zero. webhook_url/agent_mode were dropped (migration 029); only
// HITL settings remain mutable.
type UpdateAgentRequest struct {
	HITLEnabled          *bool   `json:"hitl_enabled,omitempty"`
	HITLTTLSeconds       *int    `json:"hitl_ttl_seconds,omitempty"`
	HITLExpirationAction *string `json:"hitl_expiration_action,omitempty"`
	// HITLMode is the action-gate sub-mode (Slice 7b): "all" | "high_impact".
	// Settable independently of the other HITL fields.
	HITLMode *string `json:"hitl_mode,omitempty"`
	// InboundPolicy / InboundAllowlist set the per-agent inbound ingestion gate
	// (migration 033 / Slice 7). Pointers so absent != zero.
	InboundPolicy    *string   `json:"inbound_policy,omitempty"`
	InboundAllowlist *[]string `json:"inbound_allowlist,omitempty"`
}

type updateAgentInput struct {
	Address string `path:"address"`
	Body    UpdateAgentRequest
}

func (s *Server) handleUpdateAgent(ctx context.Context, in *updateAgentInput) (*agentOutput, error) {
	// Mutating agent config (HITL, inbound_policy) is account administration —
	// an agent-scoped credential must not change its own security posture
	// (Slice 5a hard ceiling), so this is account-only even for the bound agent.
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	req := in.Body
	touched := false

	if req.HITLEnabled != nil || req.HITLTTLSeconds != nil || req.HITLExpirationAction != nil {
		enabled := ag.HITLEnabled
		if req.HITLEnabled != nil {
			enabled = *req.HITLEnabled
		}
		ttl := ag.HITLTTLSeconds
		if req.HITLTTLSeconds != nil {
			ttl = *req.HITLTTLSeconds
		}
		action := ag.HITLExpirationAction
		if req.HITLExpirationAction != nil {
			action = *req.HITLExpirationAction
		}
		if s.deps.UpdateAgentHITL == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "update unavailable")
		}
		if err := s.deps.UpdateAgentHITL(ctx, ag.ID, ag.UserID, enabled, ttl, action); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
		}
		touched = true
	}

	if req.HITLMode != nil {
		if s.deps.UpdateAgentHITLMode == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "update unavailable")
		}
		if err := s.deps.UpdateAgentHITLMode(ctx, ag.ID, ag.UserID, *req.HITLMode); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
		}
		touched = true
	}

	if req.InboundPolicy != nil || req.InboundAllowlist != nil {
		policy := ag.InboundPolicy
		if req.InboundPolicy != nil {
			policy = *req.InboundPolicy
		}
		allowlist := ag.InboundAllowlist
		if req.InboundAllowlist != nil {
			allowlist = *req.InboundAllowlist
		}
		if s.deps.UpdateAgentInboundPolicy == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "update unavailable")
		}
		if err := s.deps.UpdateAgentInboundPolicy(ctx, ag.ID, ag.UserID, policy, allowlist); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
		}
		touched = true
	}

	if !touched {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "no recognized fields in request")
	}

	// Re-read for the authoritative post-update state (ag.ID is the email).
	updated, err := s.deps.GetAgent(ctx, ag.ID)
	if err != nil || updated == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to reload agent")
	}
	return &agentOutput{Body: agentViewFromIdentity(updated)}, nil
}

type deleteAgentOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func (s *Server) handleDeleteAgent(ctx context.Context, in *AddressParam) (*deleteAgentOutput, error) {
	// Deleting an agent is account administration — barred for agent-scoped
	// credentials even on their own bound agent (Slice 5a hard ceiling).
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.DeleteAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "delete unavailable")
	}
	if err := s.deps.DeleteAgent(ctx, ag.ID, ag.UserID); err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete agent")
	}
	out := &deleteAgentOutput{}
	out.Body.Status = "deleted"
	return out, nil
}

func (s *Server) handleCreateAgent(ctx context.Context, in *createAgentInput) (*createAgentOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	req := in.Body
	email := identity.NormalizeEmail(req.Email)

	// Shared-domain registration via slug.
	isShared := false
	if req.Slug != "" {
		if s.deps.SharedDomain == "" {
			return nil, NewError(http.StatusBadRequest, "slug_registration_disabled", "shared-domain registration is not configured")
		}
		if err := validateSlug(req.Slug); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_slug", err.Error())
		}
		email = req.Slug + "@" + s.deps.SharedDomain
		isShared = true
	}

	if email == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "email is required")
	}

	// Resolve the DNS domain.
	var domain string
	if isShared {
		domain = s.deps.SharedDomain
	} else {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) != 2 || parts[1] == "" {
			return nil, NewError(http.StatusBadRequest, "invalid_request", "invalid email address")
		}
		domain = parts[1]
	}

	// Custom-domain ownership guard (decision 1): the domain must be
	// registered to this user AND verified. This is the load-bearing
	// authorization that an agent can only be created on a domain the
	// caller controls.
	if !isShared {
		if s.deps.LookupDomain == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "domain lookup unavailable")
		}
		dom, err := s.deps.LookupDomain(ctx, domain, user.ID)
		if err != nil {
			return nil, NewError(http.StatusBadRequest, "domain_not_registered", "register and verify your domain first")
		}
		if !dom.Verified {
			return nil, NewError(http.StatusBadRequest, "domain_not_verified", "verify your domain first")
		}
	}

	// Per-user agent cap (after auth + domain checks, so a 402 means
	// "valid request, out of capacity" — never masks a 400/401).
	if s.deps.EnforceAgentCreate != nil {
		if err := s.deps.EnforceAgentCreate(ctx, user.ID); err != nil {
			if env, ok := limitEnvelope(err); ok {
				return nil, env
			}
			return nil, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
		}
	}

	if s.deps.CreateAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "agent create unavailable")
	}
	// webhookURL/agentMode params are ignored by the store (migration 029);
	// pass "" to satisfy the retained signature.
	ag, err := s.deps.CreateAgent(ctx, email, domain, req.Name, "", "", user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return nil, NewError(http.StatusConflict, "conflict", "agent already registered for this domain")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to register agent")
	}
	return &createAgentOutput{Body: CreateAgentResponse{ID: ag.ID, Domain: ag.Domain, Email: ag.Email}}, nil
}

// limitEnvelope translates a limits.LimitExceededError into a 402 envelope
// (code "limit_exceeded") carrying the structured cap details, replacing the
// legacy bespoke LimitErrorBody with the standardized envelope.
func limitEnvelope(err error) (*ErrorEnvelope, bool) {
	le, ok := limits.IsLimitExceeded(err)
	if !ok {
		return nil, false
	}
	return NewError(http.StatusPaymentRequired, "limit_exceeded", le.Error()).WithDetails(map[string]any{
		"resource":    le.Resource,
		"limit":       le.Limit,
		"current":     le.Current,
		"plan_code":   le.Limits.PlanCode,
		"upgrade_url": le.Limits.UpgradeURL,
	}), true
}
