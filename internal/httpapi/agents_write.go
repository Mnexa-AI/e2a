package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/limits"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) — the duplicate-key signal on the
// agent_identities PK. Mirrors identity.isUniqueViolation / the agent
// package's helper; typed instead of a strings.Contains match so a non-pg
// error (e.g. a wrapped network error) can't masquerade as a conflict.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

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
	Email string `json:"email" maxLength:"320" doc:"The agent's full email address — its identity. At most 320 characters (Unicode code points)."`
	Name  string `json:"name,omitempty" maxLength:"200" doc:"Display name for the agent (a UI label; the agent's identity is its email). At most 200 characters (Unicode code points) — the same cap as updateAgent."`
}

type createAgentInput struct {
	Body CreateAgentRequest
}

// createAgentOutput returns the full AgentView (AG-5) — one agent shape across
// create/get/update/list, so a caller never needs a follow-up GET — plus any
// non-fatal create-time warnings.
type createAgentOutput struct {
	Body createAgentBody
}

// createAgentBody is the shared AgentView plus a create-only `warnings` array.
// Warnings live HERE (not on AgentView) so list/get responses are unchanged;
// the field is create-scoped. Currently the only warning is the subdomain
// MX-coverage advisory (see subdomainMXCoverageWarning).
type createAgentBody struct {
	AgentView
	Warnings []string `json:"warnings,omitempty" doc:"Non-fatal advisories about this newly created agent. Present only when the create surfaced a caveat worth acting on. Currently: a subdomain agent created under a verified PARENT domain whose inbound MX coverage — an MX on the subdomain, or a wildcard MX on the parent, pointing at the e2a relay — could not be confirmed, so the inbox will not receive mail until that record is published. Advisory only (best-effort DNS check; RFC 4592 wildcard shadowing makes detection imperfect); creation still succeeds and send-only agents can ignore it. Open set; tolerate unknown entries."`
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
		Description: "Move an agent the caller owns to the trash. Requires ?confirm=DELETE. A trashed agent stops receiving mail, disappears from lists, and its held messages leave the review queue; restore it via POST /v1/agents/{email}/restore within the trash retention window — 30 days by default (deployment-configurable) — after which it is purged permanently (messages included). While the agent sits in the trash its messages' expiry clocks are paused; restore resumes them exactly where they stopped. Pass permanent=true to skip the trash and delete irreversibly right away (accepts live and trashed agents). Returns 200 with a deletion receipt; messages_deleted is zero when the agent is moved to trash.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteAgent)

	huma.Register(s.API, huma.Operation{
		OperationID: "restoreAgent",
		Method:      http.MethodPost,
		Path:        "/v1/agents/{email}/restore",
		Summary:     "Restore an agent from the trash",
		Description: "Bring a trashed (soft-deleted) agent back into service, messages and configuration intact. The agent's live messages resume their clocks exactly where they stopped: each message's expires_at — and, for drafts still held for review, approval_expires_at — is shifted forward by the time the agent spent in the trash, so a restore never resurrects an inbox whose mail immediately expires, and a review hold can never lapse the instant the agent returns. Returns the restored agent. 409 not_in_trash when the agent is not in the trash.",
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleRestoreAgent)
}

// UpdateAgentRequest is the /v1 agent PATCH body. The per-agent screening/HITL
// config moved to the /v1/agents/{email}/protection sub-resource (design
// 2026-06-22), so the only mutable field left on the agent itself is the
// display name. Pointer so absent != "" (an empty name is a valid clear).
type UpdateAgentRequest struct {
	Name *string `json:"name,omitempty" maxLength:"200" doc:"New display name for the agent (a UI label; the agent's identity is its email). At most 200 characters (Unicode code points) — the same cap as createAgent."`
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
// takes it out of service immediately (held drafts leave the review queue,
// its credentials stop resolving), so it requires ?confirm=DELETE — uniform
// with every other delete op (see DeleteConfirm). The default delete is SOFT
// (trash, restorable for 30 days); permanent=true is the irreversible hard
// delete and also accepts an agent already in the trash ("delete forever").
type deleteAgentInput struct {
	Address   string `path:"email"`
	Confirm   string `query:"confirm" enum:"DELETE" required:"true" doc:"Must be the literal DELETE. The default action moves the agent to trash; permanent=true is irreversible."`
	Permanent bool   `query:"permanent" doc:"Delete irreversibly right away instead of moving to the trash. Accepts live and trashed agents."`
}

func (s *Server) handleDeleteAgent(ctx context.Context, in *deleteAgentInput) (*deleteAgentOutput, error) {
	// Deleting an agent is account administration — barred for agent-scoped
	// credentials even on their own bound agent (Slice 5a hard ceiling).
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	// Confirm is enforced declaratively by Huma (required + enum:[DELETE]): a
	// missing/wrong ?confirm is a 422 before this handler.
	//
	// One resolution path across both variants: any-state, so permanent=true
	// can purge an agent already in the trash. A trashed agent is 404 for the
	// SOFT delete — matching every live lookup's view of it.
	ag, err := s.resolveOwnedAgentAnyState(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	var messagesDeleted int64
	if in.Permanent {
		if s.deps.PermanentDeleteAgent == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "delete unavailable")
		}
		messagesDeleted, err = s.deps.PermanentDeleteAgent(ctx, ag.ID, ag.UserID)
	} else if ag.DeletedAt != nil {
		return nil, NewError(http.StatusNotFound, "not_found", "agent not found")
	} else {
		if s.deps.DeleteAgent == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "delete unavailable")
		}
		err = s.deps.DeleteAgent(ctx, ag.ID, ag.UserID)
	}
	if err != nil {
		// A not-found here means the agent vanished (hard-deleted or moved to
		// the trash by a concurrent request) between the any-state resolve and
		// the mutation — 404, not a generic 500.
		if errors.Is(err, identity.ErrAgentNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "agent not found")
		}
		if errors.Is(err, identity.ErrSendInProgress) {
			return nil, NewError(http.StatusConflict, "send_in_progress",
				"agent has an outbound send in progress; retry permanent deletion after it finishes")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete agent")
	}
	// ag.ID is the agent's email (canonical form) — echo it as the identity key.
	return &deleteAgentOutput{Body: DeleteAgentResult{
		Deleted:         true,
		Email:           ag.ID,
		MessagesDeleted: messagesDeleted,
	}}, nil
}

// handleRestoreAgent brings a trashed agent back (POST
// /v1/agents/{email}/restore). Account administration, like delete.
func (s *Server) handleRestoreAgent(ctx context.Context, in *AddressParam) (*agentOutput, error) {
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	if s.deps.RestoreAgent == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "restore unavailable")
	}
	ag, err := s.resolveOwnedAgentAnyState(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	// The trash-state decision belongs to the store (its UPDATE is the one
	// atomic check); the handler only maps the sentinel.
	if err := s.deps.RestoreAgent(ctx, ag.ID, ag.UserID); err != nil {
		if errors.Is(err, identity.ErrNotInTrash) {
			return nil, NewError(http.StatusConflict, "not_in_trash", "agent is not in the trash")
		}
		if errors.Is(err, identity.ErrAgentNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "agent not found")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to restore agent")
	}
	// Re-read via the LIVE getter for the authoritative post-restore state
	// (ag.ID is the email); it also proves the agent is visible again.
	restored, err := s.deps.GetAgent(ctx, ag.ID)
	if err != nil || restored == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to reload agent")
	}
	return &agentOutput{Body: agentViewFromIdentity(restored)}, nil
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
	subdomain := domain // the agent's literal address domain, before any parent rebind
	viaParent := false  // true when the agent was authorized via a covering parent
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
	//
	// Resolution precedence (subdomain agents): an EXACT registered-domain
	// row wins first — so an exact row that is unverified still yields
	// domain_not_verified and is never masked by a covering parent. Only when
	// no exact row exists do we fall back to the most-specific VERIFIED parent
	// the user owns (label-boundary covered — see LookupCoveringDomain). AWS
	// SES DKIM-signs and DMARC-aligns subdomain mail under the parent identity,
	// so a subdomain agent needs no separate registration. On a parent match we
	// rebind `domain` to the PARENT: it is what gets stored in
	// agent_identities.domain, satisfying the FK and making the quota JOIN, the
	// DKIM signer, and the sending-status lookup all resolve to the verified
	// parent while the agent keeps its full subdomain address.
	if !isShared {
		if s.deps.LookupDomain == nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "domain lookup unavailable")
		}
		dom, err := s.deps.LookupDomain(ctx, domain, user.ID)
		if err == nil {
			// Exact registered row exists — preserve today's behavior exactly.
			if !dom.Verified {
				return nil, NewError(http.StatusBadRequest, "domain_not_verified", "verify your domain first")
			}
		} else {
			// No exact row: try a verified covering parent before rejecting.
			var parent *identity.Domain
			if s.deps.LookupCoveringDomain != nil {
				parent, _ = s.deps.LookupCoveringDomain(ctx, domain, user.ID)
			}
			if parent == nil {
				return nil, NewError(http.StatusBadRequest, "domain_not_registered", "register and verify your domain first")
			}
			domain = parent.Domain // bind the agent to the verified parent identity
			viaParent = true
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
		if isUniqueViolation(err) {
			// Soft-delete (migration 063) keeps the trashed row's PK, so the
			// address stays reserved: a user who trashed an inbox and tries to
			// recreate the same address hits this conflict. If the duplicate is
			// the caller's own trashed inbox, point them at the trash (restore
			// or permanently delete to reuse the address); otherwise fall back
			// to the generic conflict so we never reveal another account's
			// trashed inbox. Probe is post-conflict, so there's no TOCTOU
			// window between a pre-check and the INSERT.
			if s.deps.GetAgentAnyState != nil {
				if existing, lerr := s.deps.GetAgentAnyState(ctx, email); lerr == nil && existing != nil &&
					existing.DeletedAt != nil && existing.UserID == user.ID {
					return nil, NewError(http.StatusConflict, "address_in_trash",
						"this address belongs to an inbox in your trash — restore it, or delete it permanently from the trash, to reuse the address")
				}
			}
			// agent_taken joins the *_taken conflict family (domain_taken,
			// alias_taken): the requested address is already registered.
			return nil, NewError(http.StatusConflict, "agent_taken", "agent already registered for this domain")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to register agent")
	}
	body := createAgentBody{AgentView: agentViewFromIdentity(ag)}
	// Advisory only (never fatal): a subdomain agent authorized via a covering
	// parent needs a subdomain MX (explicit or a wildcard on the parent) to
	// RECEIVE mail — MX records don't inherit. Warn if we can't confirm coverage
	// so the inbox isn't silently mail-less; a send-only agent can ignore it.
	if viaParent {
		if w := s.subdomainMXCoverageWarning(ctx, ag.EmailAddress(), subdomain); w != "" {
			body.Warnings = append(body.Warnings, w)
		}
	}
	return &createAgentOutput{Body: body}, nil
}

// subdomainMXCoverageWarning best-effort checks whether inbound mail for a
// subdomain agent will actually reach the e2a relay, returning a human-readable
// advisory when it cannot confirm coverage (empty string = coverage confirmed,
// or the check is disabled/not applicable). It is NEVER fatal — send-only
// agents are legitimate and the user may publish the record afterward.
//
// One MX lookup on the subdomain FQDN answers both shapes at once: a resolver
// synthesizes a parent wildcard (`*.<parent> MX`) for the queried subdomain, so
// an explicit subdomain MX and a wildcard-on-parent are indistinguishable here —
// and that is fine, we only care whether SOME MX routes to the relay. Detection
// is deliberately best-effort: per RFC 4592, an explicit non-MX record on the
// subdomain SHADOWS the parent wildcard (the resolver then returns no MX for the
// name even though a wildcard exists), and transient DNS failures look the same
// as "no record", so a false warning is possible. Hence advisory, not a gate.
func (s *Server) subdomainMXCoverageWarning(ctx context.Context, agentEmail, subdomain string) string {
	relay := strings.TrimSuffix(strings.ToLower(s.deps.SMTPDomain), ".")
	if s.deps.ResolveMX == nil || relay == "" {
		return "" // no resolver / no configured relay host — cannot check, stay silent
	}
	// Bound the probe so a slow/hanging resolver never stalls agent creation.
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	hosts, err := s.deps.ResolveMX(probeCtx, subdomain)
	if err == nil {
		for _, h := range hosts {
			if strings.EqualFold(strings.TrimSuffix(h, "."), relay) {
				return "" // an MX for this subdomain routes to the relay — covered
			}
		}
	}
	// No confirmable coverage (no matching MX, or the lookup failed).
	parent := subdomain
	if i := strings.IndexByte(subdomain, '.'); i >= 0 {
		parent = subdomain[i+1:]
	}
	return fmt.Sprintf(
		"inbound MX coverage for %s could not be confirmed: %s will not receive email until you publish an MX record routing this subdomain to %s — publish a wildcard \"*.%s MX 10 %s\" to cover all subdomains at once, or an MX on %s. This does not affect sending.",
		agentEmail, agentEmail, relay, parent, relay, subdomain,
	)
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
