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
// CreateAgentRequest is the create-agent body (AG-1/AG-2). `email` is required
// and is the single create path: a custom-domain agent uses an email on a
// verified domain the caller owns; a shared-domain agent is just an email on
// the deployment's shared domain (e.g. xyz@agents.e2a.dev) — detected by the
// domain, not a separate `slug` field. The legacy `slug` field is dropped.
type CreateAgentRequest struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type createAgentInput struct {
	Body CreateAgentRequest
}

// createAgentOutput returns the full AgentView (AG-5) — one agent shape across
// create/get/update/list, so a caller never needs a follow-up GET.
type createAgentOutput struct {
	Body AgentView
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
		Description:   "Register an agent by full email. A custom-domain agent's domain must be a verified domain the caller owns; an email on the deployment's shared domain (e.g. xyz@agents.e2a.dev) is registered as a shared-domain agent. Returns the full agent.",
		Tags:          []string{"agents"},
		Security:      []map[string][]string{{"bearer": {}}},
		DefaultStatus: http.StatusCreated,
		Responses: map[string]*huma.Response{
			"402":     s.limitExceededResponse(),
			"429":     s.rateLimitedResponse(),
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleCreateAgent)

	huma.Register(s.API, huma.Operation{
		OperationID: "updateAgent",
		Method:      http.MethodPatch,
		Path:        "/v1/agents/{email}",
		Summary:     "Update an agent",
		Description: "Update an agent's display name. The screening/protection config lives on the /v1/agents/{email}/protection sub-resource. Returns the post-update agent.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleUpdateAgent)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteAgent",
		Method:      http.MethodDelete,
		Path:        "/v1/agents/{email}",
		Summary:     "Delete an agent",
		Description: "Delete an agent the caller owns. Requires ?confirm=DELETE (irreversible). Returns 200 with a deletion receipt ({deleted:true, email, messages_deleted}) — the cascade also removes the agent's webhook-delivery records and revokes its credentials.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteAgent)
}

// UpdateAgentRequest is the /v1 agent PATCH body. The per-agent screening/HITL
// config moved to the /v1/agents/{email}/protection sub-resource (design
// 2026-06-22), so the only mutable field left on the agent itself is the
// display name. Pointer so absent != "" (an empty name is a valid clear).
type UpdateAgentRequest struct {
	Name *string `json:"name,omitempty" maxLength:"200" doc:"New display name for the agent (a UI label; the agent's identity is its email)."`
}

type updateAgentInput struct {
	Address string `path:"email"`
	Body    UpdateAgentRequest
}

func (s *Server) handleUpdateAgent(ctx context.Context, in *updateAgentInput) (*agentOutput, error) {
	// Mutating an agent is account administration — an agent-scoped credential
	// must not rename its own agent (Slice 5a hard ceiling), so this is
	// account-only even for the bound agent. (Screening posture moved to the
	// account-scoped /protection sub-resource; only the display name is left.)
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if in.Body.Name == nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "no recognized fields in request")
	}
	if s.deps.UpdateAgentName == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "update unavailable")
	}
	if err := s.deps.UpdateAgentName(ctx, ag.ID, ag.UserID, *in.Body.Name); err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
	}

	// Re-read for the authoritative post-update state (ag.ID is the email).
	updated, err := s.deps.GetAgent(ctx, ag.ID)
	if err != nil || updated == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to reload agent")
	}
	return &agentOutput{Body: agentViewFromIdentity(updated)}, nil
}

type deleteAgentOutput struct{ Body DeleteAgentResult }

// deleteAgentInput adds the confirmation guard (AG-6). Deleting an agent
// discards held drafts and revokes its credentials, so it requires
// ?confirm=DELETE — uniform with every other delete op (see DeleteConfirm).
type deleteAgentInput struct {
	Address string `path:"email"`
	DeleteConfirm
}

func (s *Server) handleDeleteAgent(ctx context.Context, in *deleteAgentInput) (*deleteAgentOutput, error) {
	// Deleting an agent is account administration — barred for agent-scoped
	// credentials even on their own bound agent (Slice 5a hard ceiling).
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	// Confirm is enforced declaratively by Huma (required + enum:[DELETE] on
	// DeleteConfirm): a missing/wrong ?confirm is a 422 before this handler.
	if s.deps.DeleteAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "delete unavailable")
	}
	messagesDeleted, err := s.deps.DeleteAgent(ctx, ag.ID, ag.UserID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete agent")
	}
	// ag.ID is the agent's email (canonical form) — echo it as the identity key.
	return &deleteAgentOutput{Body: DeleteAgentResult{
		Deleted:         true,
		Email:           ag.ID,
		MessagesDeleted: messagesDeleted,
	}}, nil
}

func (s *Server) handleCreateAgent(ctx context.Context, in *createAgentInput) (*createAgentOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	req := in.Body
	email := identity.NormalizeEmail(req.Email)

	if email == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "email is required")
	}

	// Resolve the DNS domain from the email itself (AG-1/AG-2): there is one
	// create path. An email on the deployment's shared domain is a shared-domain
	// registration (its local-part is validated as a slug, no ownership check);
	// any other domain is a custom-domain agent gated by ownership below.
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "invalid email address")
	}
	domain := parts[1]
	isShared := s.deps.SharedDomain != "" && strings.EqualFold(domain, s.deps.SharedDomain)
	if isShared {
		domain = s.deps.SharedDomain // normalize to the configured casing
		if err := validateSlug(parts[0]); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_slug", err.Error())
		}
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
			// agent_taken joins the *_taken conflict family (domain_taken,
			// alias_taken): the requested address is already registered.
			return nil, NewError(http.StatusConflict, "agent_taken", "agent already registered for this domain")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to register agent")
	}
	return &createAgentOutput{Body: agentViewFromIdentity(ag)}, nil
}

// limitEnvelope translates a limits.LimitExceededError into a 402 envelope
// (code "limit_exceeded") carrying a typed LimitExceededDetails payload. The
// details.resource is an AccountView usage/limits field stem so a client can key
// the error straight to usage.<resource> / limits.max_<resource>; the declared
// 402 schema on the cap-enforcing operations is LimitExceededEnvelope.
func limitEnvelope(err error) (*ErrorEnvelope, bool) {
	le, ok := limits.IsLimitExceeded(err)
	if !ok {
		return nil, false
	}
	return NewError(http.StatusPaymentRequired, "limit_exceeded", le.Error()).WithDetails(LimitExceededDetails{
		Resource:   le.Resource,
		Limit:      int64(le.Limit),
		Current:    int64(le.Current),
		PlanCode:   le.Limits.PlanCode,
		UpgradeURL: le.Limits.UpgradeURL,
	}), true
}
