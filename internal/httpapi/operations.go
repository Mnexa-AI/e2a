package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// --- GET /v1/info -----------------------------------------------------------

// DeploymentInfoView mirrors the legacy /api/v1/info body verbatim. Slice 1
// is consistency + path move only — no shape change (shape cleanup is a
// later slice), so the field set and json tags match the current contract.
type DeploymentInfoView struct {
	SharedDomain            string `json:"shared_domain"`
	SlugRegistrationEnabled bool   `json:"slug_registration_enabled"`
	PublicURL               string `json:"public_url,omitempty"`
}

type infoOutput struct {
	Body DeploymentInfoView
}

func (s *Server) registerInfo() {
	huma.Register(s.API, huma.Operation{
		OperationID: "getInfo",
		Method:      http.MethodGet,
		Path:        "/v1/info",
		Summary:     "Deployment info",
		Description: "Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.",
		Tags:        []string{"meta"},
	}, func(ctx context.Context, _ *struct{}) (*infoOutput, error) {
		return &infoOutput{Body: DeploymentInfoView{
			SharedDomain:            s.deps.SharedDomain,
			SlugRegistrationEnabled: s.deps.SharedDomain != "",
			PublicURL:               s.deps.PublicURL,
		}}, nil
	})
}

// --- GET /v1/agents ---------------------------------------------------------

// AgentView is the public representation of an agent. It mirrors the legacy
// AgentInfo json shape exactly for Slice 1; field removals (webhook_url,
// agent_mode — decisions 2/3) happen in the agent-model slice, not here.
type AgentView struct {
	ID                   string    `json:"id"`
	Domain               string    `json:"domain"`
	Email                string    `json:"email"`
	Name                 string    `json:"name"`
	WebhookURL           string    `json:"webhook_url"`
	AgentMode            string    `json:"agent_mode"`
	DomainVerified       bool      `json:"domain_verified"`
	CreatedAt            time.Time `json:"created_at"`
	HITLEnabled          bool      `json:"hitl_enabled"`
	HITLTTLSeconds       int       `json:"hitl_ttl_seconds"`
	HITLExpirationAction string    `json:"hitl_expiration_action"`
}

// agentViewFromIdentity maps the storage record to the public view. Kept in
// lockstep with the legacy agentInfoFromIdentity so both surfaces emit an
// identical agent shape during the transition.
func agentViewFromIdentity(ag *identity.AgentIdentity) AgentView {
	return AgentView{
		ID:                   ag.ID,
		Domain:               ag.Domain,
		Email:                ag.EmailAddress(),
		Name:                 ag.Name,
		WebhookURL:           ag.WebhookURL,
		AgentMode:            ag.AgentMode,
		DomainVerified:       ag.DomainVerified,
		CreatedAt:            ag.CreatedAt,
		HITLEnabled:          ag.HITLEnabled,
		HITLTTLSeconds:       ag.HITLTTLSeconds,
		HITLExpirationAction: ag.HITLExpirationAction,
	}
}

type listAgentsOutput struct {
	Body struct {
		Agents []AgentView `json:"agents"`
	}
}

func (s *Server) registerAgents() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listAgents",
		Method:      http.MethodGet,
		Path:        "/v1/agents",
		Summary:     "List agents",
		Description: "List the agents owned by the authenticated account.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, func(ctx context.Context, _ *struct{}) (*listAgentsOutput, error) {
		user, err := s.requireUser(ctx)
		if err != nil {
			return nil, err
		}
		agents, err := s.deps.ListAgents(ctx, user.ID)
		if err != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list agents")
		}
		out := &listAgentsOutput{}
		out.Body.Agents = make([]AgentView, 0, len(agents))
		for i := range agents {
			out.Body.Agents = append(out.Body.Agents, agentViewFromIdentity(&agents[i]))
		}
		return out, nil
	})
}
