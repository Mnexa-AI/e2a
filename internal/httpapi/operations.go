package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// --- GET /v1/info -----------------------------------------------------------

// APIVersion is the public /v1 contract version. Single source for both the
// OpenAPI document version (huma.DefaultConfig) and the GET /v1/info `version`
// field; tracks the repo-root VERSION file.
const APIVersion = "1.0.0"

// DeploymentInfoView is the public, unauthenticated deployment-discovery body.
// `version` (I-1) lets clients detect the API contract version pre-auth — the
// cheapest forward-compatibility lever.
type DeploymentInfoView struct {
	Version                 string `json:"version"`
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
			Version:                 APIVersion,
			SharedDomain:            s.deps.SharedDomain,
			SlugRegistrationEnabled: s.deps.SharedDomain != "",
			PublicURL:               s.deps.PublicURL,
		}}, nil
	})
}

// --- GET /v1/agents ---------------------------------------------------------

// AgentView is the public representation of an agent. The legacy webhook_url
// and agent_mode fields were dropped (migration 029). The per-agent
// screening/HITL config moved to the account-scoped
// /v1/agents/{email}/protection sub-resource (design 2026-06-22), so AgentView
// is identity + status only — an agent-scoped credential reading its own agent
// no longer learns its detection tuning (closes audit #13).
type AgentView struct {
	ID             string    `json:"id"`
	Domain         string    `json:"domain"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	DomainVerified bool      `json:"domain_verified"`
	CreatedAt      time.Time `json:"created_at"`
}

// agentViewFromIdentity maps the storage record to the public view.
func agentViewFromIdentity(ag *identity.AgentIdentity) AgentView {
	return AgentView{
		ID:             ag.ID,
		Domain:         ag.Domain,
		Email:          ag.EmailAddress(),
		Name:           ag.Name,
		DomainVerified: ag.DomainVerified,
		CreatedAt:      ag.CreatedAt,
	}
}

// listAgentsOutput uses the shared Page[T] envelope (items + next_cursor). The
// list is keyset-paginated on (created_at, id) — same cursor scheme as every
// other v1 collection.
type listAgentsOutput struct {
	Body Page[AgentView]
}

// listAgentsInput carries the standard cursor/limit (PageParams).
type listAgentsInput struct {
	PageParams
}

// AddressParam is the shared path input for per-agent operations. The
// address is the agent's full email and the resource identifier
// (api-v1-redesign decision 1); Huma URL-decodes it from the path.
type AddressParam struct {
	Address string `path:"email" doc:"The agent's full email address, e.g. support@acme.com."`
}

type agentOutput struct {
	Body AgentView
}

func (s *Server) registerAgents() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listAgents",
		Method:      http.MethodGet,
		Path:        "/v1/agents",
		Summary:     "List agents",
		Description: "List the agents owned by the authenticated account, newest first, with cursor pagination.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListAgents)

	huma.Register(s.API, huma.Operation{
		OperationID: "getAgent",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{email}",
		Summary:     "Get an agent",
		Description: "Fetch a single agent the authenticated account owns, by full email address.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, func(ctx context.Context, in *AddressParam) (*agentOutput, error) {
		ag, err := s.resolveOwnedAgent(ctx, in.Address)
		if err != nil {
			return nil, err
		}
		return &agentOutput{Body: agentViewFromIdentity(ag)}, nil
	})
}

func (s *Server) handleListAgents(ctx context.Context, in *listAgentsInput) (*listAgentsOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	afterCreatedAt, afterID, err := s.decodeKeyset(in.Cursor)
	if err != nil {
		return nil, err
	}
	limit := effectiveLimit(in.Limit)
	// Fetch limit+1 to detect a further page.
	agents, err := s.deps.ListAgents(ctx, user.ID, limit+1, afterCreatedAt, afterID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list agents")
	}
	hasMore := len(agents) > limit
	if hasMore {
		agents = agents[:limit]
	}
	items := make([]AgentView, 0, len(agents))
	for i := range agents {
		items = append(items, agentViewFromIdentity(&agents[i]))
	}
	var nextCursor string
	if hasMore {
		last := agents[len(agents)-1]
		if nextCursor, err = s.encodeKeyset(last.CreatedAt, last.ID); err != nil {
			return nil, err
		}
	}
	return &listAgentsOutput{Body: NewPage(items, nextCursor)}, nil
}

// resolveOwnedAgent authenticates the caller, loads the agent by address,
// and verifies ownership — the shared front half of every per-agent
// operation. A missing OR non-owned agent is reported as 404 not_found,
// consistent with every other per-resource lookup on the surface (messages,
// domains, webhooks, templates, events, conversations, reviews). The two
// cases are deliberately conflated into one indistinguishable 404 so the
// response never reveals that another account's agent exists (anti-enumeration).
// A genuine scope-403 — an agent-scoped credential reaching a different agent —
// is a distinct case handled below and is NOT collapsed into the 404.
func (s *Server) resolveOwnedAgent(ctx context.Context, address string) (*identity.AgentIdentity, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.GetAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "agent lookup unavailable")
	}
	ag, err := s.deps.GetAgent(ctx, identity.NormalizeEmail(address))
	if err != nil || ag == nil || ag.UserID != p.User.ID {
		return nil, NewError(http.StatusNotFound, "not_found", "agent not found")
	}
	// Hard scope ceiling (Slice 5a): an agent-scoped credential is pinned to a
	// single agent. Even though the owner owns this agent, a credential bound
	// to a DIFFERENT agent must not act here. Account-scoped credentials pass.
	// This is the one choke point for every per-agent operation.
	if p.Scope == identity.ScopeAgent && p.AgentID != ag.ID {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"this agent-scoped credential is bound to a different agent")
	}
	return ag, nil
}
