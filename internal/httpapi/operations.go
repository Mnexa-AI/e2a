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
// and agent_mode fields were dropped (migration 029): push is delivered solely
// via the /v1/webhooks subscriber resource and WebSocket is open to all agents.
type AgentView struct {
	ID                   string    `json:"id"`
	Domain               string    `json:"domain"`
	Email                string    `json:"email"`
	Name                 string    `json:"name"`
	DomainVerified       bool      `json:"domain_verified"`
	CreatedAt            time.Time `json:"created_at"`
	// hitl_enabled / hitl_mode were retired in Slice 5b (see outbound_policy /
	// outbound_scan). The HITL review-queue mechanism fields survive.
	HITLTTLSeconds       int    `json:"hitl_ttl_seconds"`
	HITLExpirationAction string `json:"hitl_expiration_action" enum:"approve,reject"`
	// InboundPolicy is the per-agent inbound ingestion gate (migration 033 /
	// Slice 7): one of open, allowlist, domain, verified_only. InboundAllowlist
	// holds the trusted addresses (allowlist) or domains (domain); omitted when
	// empty.
	InboundPolicy    string   `json:"inbound_policy" enum:"open,allowlist,domain,verified_only"`
	InboundAllowlist []string `json:"inbound_allowlist,omitempty" nullable:"false"`
	// Screening config (migration 038 / Slice 3): producer-policy actions, the
	// outbound recipient gate, and the inbound/outbound content scans with their
	// review/block threshold ladder.
	InboundPolicyAction         string   `json:"inbound_policy_action" enum:"flag,review,block"`
	OutboundPolicy              string   `json:"outbound_policy" enum:"open,allowlist,domain"`
	OutboundAllowlist           []string `json:"outbound_allowlist,omitempty" nullable:"false"`
	OutboundPolicyAction        string   `json:"outbound_policy_action" enum:"flag,review,block"`
	InboundScan                 string   `json:"inbound_scan" enum:"off,on"`
	InboundScanReviewThreshold  float64  `json:"inbound_scan_review_threshold"`
	InboundScanBlockThreshold   float64  `json:"inbound_scan_block_threshold"`
	OutboundScan                string   `json:"outbound_scan" enum:"off,on"`
	OutboundScanReviewThreshold float64  `json:"outbound_scan_review_threshold"`
	OutboundScanBlockThreshold  float64  `json:"outbound_scan_block_threshold"`
}

// agentViewFromIdentity maps the storage record to the public view.
func agentViewFromIdentity(ag *identity.AgentIdentity) AgentView {
	return AgentView{
		ID:                   ag.ID,
		Domain:               ag.Domain,
		Email:                ag.EmailAddress(),
		Name:                 ag.Name,
		DomainVerified:       ag.DomainVerified,
		CreatedAt:            ag.CreatedAt,
		HITLTTLSeconds:       ag.HITLTTLSeconds,
		HITLExpirationAction: ag.HITLExpirationAction,
		InboundPolicy:        ag.InboundPolicy,
		InboundAllowlist:     ag.InboundAllowlist,

		InboundPolicyAction:         ag.InboundPolicyAction,
		OutboundPolicy:              ag.OutboundPolicy,
		OutboundAllowlist:           ag.OutboundAllowlist,
		OutboundPolicyAction:        ag.OutboundPolicyAction,
		InboundScan:                 ag.InboundScan,
		InboundScanReviewThreshold:  ag.InboundScanReviewThreshold,
		InboundScanBlockThreshold:   ag.InboundScanBlockThreshold,
		OutboundScan:                ag.OutboundScan,
		OutboundScanReviewThreshold: ag.OutboundScanReviewThreshold,
		OutboundScanBlockThreshold:  ag.OutboundScanBlockThreshold,
	}
}

// listAgentsOutput uses the shared Page[T] envelope (items + next_cursor) so
// the list has a pagination slot from GA day one. next_cursor is null at launch
// (all agents are returned in one page); wiring real cursoring + a server-side
// limit later is additive — no response-shape change. (GA blocker #3.)
type listAgentsOutput struct {
	Body Page[AgentView]
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
		Description: "List the agents owned by the authenticated account.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, func(ctx context.Context, _ *struct{}) (*listAgentsOutput, error) {
		user, err := s.requireAccountUser(ctx)
		if err != nil {
			return nil, err
		}
		agents, err := s.deps.ListAgents(ctx, user.ID)
		if err != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list agents")
		}
		items := make([]AgentView, 0, len(agents))
		for i := range agents {
			items = append(items, agentViewFromIdentity(&agents[i]))
		}
		return &listAgentsOutput{Body: NewPage(items, "")}, nil
	})

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

// resolveOwnedAgent authenticates the caller, loads the agent by address,
// and verifies ownership — the shared front half of every per-agent
// operation. It mirrors the legacy resolveAgentForUser behavior: a missing
// or non-owned agent is reported as 403 (the legacy surface does not
// distinguish the two, and preserving that is a Slice-1 non-goal to change).
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
		return nil, NewError(http.StatusForbidden, "forbidden", "agent not found")
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
