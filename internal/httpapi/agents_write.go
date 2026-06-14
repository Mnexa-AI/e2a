package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/danielgtaylor/huma/v2"
)

// CreateAgentRequest mirrors the legacy RegisterAgentRequest verbatim. The
// agent_mode / slug / webhook_url fields are legacy and are dropped in the
// agent-model slice (decisions 1/2); Slice 1 preserves them for the path
// move + conventions only.
// Fields are schema-optional (omitempty) so validation is handler-owned and
// uniform — the legacy 400 business-rule messages, not Huma's 422 (email
// itself can't be schema-required since the slug path derives it).
type CreateAgentRequest struct {
	Email      string `json:"email,omitempty"`
	Slug       string `json:"slug,omitempty"`
	Name       string `json:"name,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`
	AgentMode  string `json:"agent_mode,omitempty"`
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
}

func (s *Server) handleCreateAgent(ctx context.Context, in *createAgentInput) (*createAgentOutput, error) {
	user, err := s.requireUser(ctx)
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
	if req.WebhookURL != "" {
		if err := agent.ValidateWebhookURL(req.WebhookURL); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_webhook_url", err.Error())
		}
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

	// agent_mode is required (legacy contract).
	mode := req.AgentMode
	if mode == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "agent_mode is required (must be 'local' or 'cloud')")
	}
	if mode != "cloud" && mode != "local" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "agent_mode must be 'cloud' or 'local'")
	}
	if mode == "cloud" && req.WebhookURL == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "webhook_url is required for cloud agent mode")
	}

	if s.deps.CreateAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "agent create unavailable")
	}
	ag, err := s.deps.CreateAgent(ctx, email, domain, req.Name, req.WebhookURL, mode, user.ID)
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
