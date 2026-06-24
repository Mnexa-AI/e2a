package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// AccountUserView is the authenticated principal's identity (A-1). Returned by
// GET /v1/account (MCP `whoami`) so an agent/operator can answer "who am I"
// without a follow-up call.
type AccountUserView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// AccountView is the authenticated account: identity + plan + limits + usage
// (A-2, formerly LimitsView). `whoami` maps here. It is scope-aware: `scope`
// is always set, and `agent_address` is populated ONLY for agent-scoped
// credentials (where the credential *is* a single agent) — omitted for
// account scope, which spans many agents.
type AccountView struct {
	User         AccountUserView `json:"user"`
	Scope        string          `json:"scope" enum:"account,agent"`
	AgentAddress string          `json:"agent_address,omitempty"`
	PlanCode     string          `json:"plan_code"`
	Limits       LimitsCapsView  `json:"limits"`
	Usage        LimitsUsageView `json:"usage"`
	UpgradeURL   string          `json:"upgrade_url"`
}

type LimitsCapsView struct {
	MaxAgents        int   `json:"max_agents"`
	MaxDomains       int   `json:"max_domains"`
	MaxMessagesMonth int   `json:"max_messages_month"`
	MaxStorageBytes  int64 `json:"max_storage_bytes"`
}

// LimitsUsageView is the current consumption snapshot.
type LimitsUsageView struct {
	Agents        int   `json:"agents"`
	Domains       int   `json:"domains"`
	MessagesMonth int   `json:"messages_month"`
	StorageBytes  int64 `json:"storage_bytes"`
}

type accountOutput struct{ Body AccountView }

func (s *Server) registerAccount() {
	huma.Register(s.API, huma.Operation{
		OperationID: "getAccount", Method: http.MethodGet, Path: "/v1/account",
		Summary: "Get account: identity + plan limits + usage (whoami)", Tags: []string{"account"},
		Description: "The authenticated principal's identity (user + scope; agent_address for agent-scoped credentials), plan caps, and current usage. Works for both account- and agent-scoped credentials. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleGetMyLimits)

	huma.Register(s.API, huma.Operation{
		OperationID: "exportAccount", Method: http.MethodGet, Path: "/v1/account/export",
		Summary: "Export your data (GDPR right-of-access)", Tags: []string{"account"},
		Description: "A JSON dump of every record the authenticated account owns.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleExportUserData)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteAccount", Method: http.MethodDelete, Path: "/v1/account",
		Summary: "Delete your account + all data (irreversible)", Tags: []string{"account"},
		Description: "Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteAccount)

	huma.Register(s.API, huma.Operation{
		OperationID: "listSuppressions", Method: http.MethodGet, Path: "/v1/account/suppressions",
		Summary: "List suppressed recipient addresses", Tags: []string{"account"},
		Description: "Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleListSuppressions)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteSuppression", Method: http.MethodDelete, Path: "/v1/account/suppressions/{address}",
		Summary: "Remove an address from the suppression list", Tags: []string{"account"},
		Description: "Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed).",
		Security:    []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusNoContent,
	}, s.handleDeleteSuppression)

	huma.Register(s.API, huma.Operation{
		OperationID: "rotateAccountSigningSecret", Method: http.MethodPost, Path: "/v1/account/signing-secret/rotate",
		Summary: "Rotate the account relay signing secret", Tags: []string{"account"},
		Description: "Hard-rotate the per-user relay signing secret used to sign inbound webhook deliveries (the X-E2A-Auth-* HMAC) and HITL approval magic-links. Use when you suspect that secret is compromised. This is a HARD rotation, not a grace-window rollover: the old secret stops signing AND verifying immediately, so deliveries already in flight that were signed with it will fail verification — that is the intended effect of compromise recovery. New deliveries use the new secret at once. This is a DIFFERENT key from the per-webhook whsec_ secret (POST /v1/webhooks/{id}/rotate-secret), which keeps a 24h grace window. Returns the new secret once. Account scope only (agent-scoped credentials get 403). Honors Idempotency-Key so a retried call replays the same secret instead of rotating twice.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleRotateAccountSigningSecret)
}

// rotateAccountSigningSecretResponse carries the freshly-minted relay
// signing secret. Shown once — persist it on receipt (rotate again to
// mint another).
type rotateAccountSigningSecretResponse struct {
	SigningSecret string `json:"signing_secret"`
	SecretPrefix  string `json:"secret_prefix"`
	CreatedAt     string `json:"created_at" format:"date-time"`
}

type rotateAccountSigningSecretOutput struct {
	Body rotateAccountSigningSecretResponse
}

// rotateAccountSigningSecretInput carries an optional Idempotency-Key so a
// retried rotate replays the first secret instead of hard-rotating twice
// (which would discard the secret the caller already stored). The op has
// no request body; the idempotency dedup hashes the route alone.
type rotateAccountSigningSecretInput struct {
	IdempotencyKey string `header:"Idempotency-Key"`
}

func (s *Server) handleRotateAccountSigningSecret(ctx context.Context, in *rotateAccountSigningSecretInput) (*rotateAccountSigningSecretOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.RotateRelaySigningSecret == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "relay signing-secret rotation is not available on this deployment")
	}
	_, body, err := runIdempotent(s, ctx, user.ID, in.IdempotencyKey,
		"/v1/account/signing-secret/rotate", nil,
		func() (int, rotateAccountSigningSecretResponse, error) {
			sec, rerr := s.deps.RotateRelaySigningSecret(ctx, user.ID)
			if rerr != nil {
				return 0, rotateAccountSigningSecretResponse{}, NewError(http.StatusInternalServerError, "internal_error", "failed to rotate signing secret")
			}
			return http.StatusOK, rotateAccountSigningSecretResponse{
				SigningSecret: sec.Secret,
				SecretPrefix:  sec.SecretPrefix,
				CreatedAt:     sec.CreatedAt.UTC().Format(time.RFC3339),
			}, nil
		})
	if err != nil {
		return nil, err
	}
	return &rotateAccountSigningSecretOutput{Body: body}, nil
}

// suppressionsOutput uses the shared Page[T] envelope (items + next_cursor);
// next_cursor is null at launch. Suppressions auto-grow on every bounce/
// complaint, so the pagination slot matters most here. See listAgentsOutput.
// (GA blocker #3.)
type suppressionsOutput struct {
	Body Page[identity.Suppression]
}

// suppressionsCursor is the opaque keyset position: the last row's
// (created_at, address). Compact keys keep the cursor short.
type suppressionsCursor struct {
	CreatedAt time.Time `json:"c"`
	Address   string    `json:"a"`
}

type listSuppressionsInput struct {
	PageParams
}

func (s *Server) handleListSuppressions(ctx context.Context, in *listSuppressionsInput) (*suppressionsOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListSuppressions == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "suppressions are not available on this deployment")
	}
	var afterCreatedAt time.Time
	var afterAddress string
	if in.Cursor != "" {
		var cur suppressionsCursor
		if err := DecodeCursor(in.Cursor, &cur); err != nil {
			return nil, NewError(http.StatusBadRequest, "invalid_cursor", "invalid pagination cursor")
		}
		afterCreatedAt = cur.CreatedAt
		afterAddress = cur.Address
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	// Fetch limit+1 to detect a further page.
	list, err := s.deps.ListSuppressions(ctx, user.ID, limit+1, afterCreatedAt, afterAddress)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list suppressions")
	}
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	var nextCursor string
	if hasMore {
		last := list[len(list)-1]
		nextCursor, err = EncodeCursor(suppressionsCursor{CreatedAt: last.CreatedAt, Address: last.Address})
		if err != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to build pagination cursor")
		}
	}
	return &suppressionsOutput{Body: NewPage(list, nextCursor)}, nil
}

type deleteSuppressionInput struct {
	Address string `path:"address"`
}
type deleteSuppressionOutput struct{ Status int }

func (s *Server) handleDeleteSuppression(ctx context.Context, in *deleteSuppressionInput) (*deleteSuppressionOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.RemoveSuppression == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "suppressions are not available on this deployment")
	}
	found, err := s.deps.RemoveSuppression(ctx, user.ID, in.Address)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to remove suppression")
	}
	if !found {
		return nil, NewError(http.StatusNotFound, "not_found", "address not on the suppression list")
	}
	return &deleteSuppressionOutput{Status: http.StatusNoContent}, nil
}

type exportOutput struct {
	ContentDisposition string `header:"Content-Disposition"`
	Body               *identity.UserExport
}

func (s *Server) handleExportUserData(ctx context.Context, _ *struct{}) (*exportOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ExportUserData == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "export unavailable")
	}
	dump, err := s.deps.ExportUserData(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to export user data")
	}
	// A-3: top-level export collections render as [] not null when empty.
	if dump != nil {
		dump.Domains = orEmpty(dump.Domains)
		dump.Agents = orEmpty(dump.Agents)
		dump.APIKeys = orEmpty(dump.APIKeys)
		dump.Messages = orEmpty(dump.Messages)
		dump.UsageEvents = orEmpty(dump.UsageEvents)
		dump.OAuthConnections = orEmpty(dump.OAuthConnections)
	}
	return &exportOutput{
		ContentDisposition: `attachment; filename="e2a-export-` + user.ID + `.json"`,
		Body:               dump,
	}, nil
}

type deleteAccountInput struct {
	Confirm string `query:"confirm" doc:"Must be DELETE — this is irreversible."`
}

type deleteAccountOutput struct {
	Body *identity.DeleteUserDataResult
}

func (s *Server) handleDeleteAccount(ctx context.Context, in *deleteAccountInput) (*deleteAccountOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if in.Confirm != "DELETE" {
		return nil, NewError(http.StatusBadRequest, "confirmation_required", "add ?confirm=DELETE to the request to proceed — this is irreversible")
	}
	if s.deps.DeleteUserData == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "delete unavailable")
	}
	res, err := s.deps.DeleteUserData(ctx, user)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete user data")
	}
	return &deleteAccountOutput{Body: res}, nil
}

func (s *Server) handleGetMyLimits(ctx context.Context, _ *struct{}) (*accountOutput, error) {
	// whoami works for BOTH scopes (A-1): an agent-scoped credential must be
	// able to learn its own identity, so this authenticates any principal
	// rather than requiring account scope.
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	user := p.User
	if s.deps.GetLimits == nil {
		return nil, NewError(http.StatusServiceUnavailable, "limits_unavailable", "limits subsystem not configured")
	}
	caps, err := s.deps.GetLimits(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "limits lookup failed")
	}
	var usage LimitsUsageView
	if s.deps.GetUsage != nil {
		usage = s.deps.GetUsage(ctx, user.ID)
	}
	// agent_address only for agent-scoped credentials (the credential IS one
	// agent; its id == its email by construction). Omitted for account scope.
	agentAddress := ""
	if p.Scope == identity.ScopeAgent {
		agentAddress = p.AgentID
	}
	return &accountOutput{Body: AccountView{
		User:         AccountUserView{ID: user.ID, Email: user.Email},
		Scope:        p.Scope,
		AgentAddress: agentAddress,
		PlanCode:     caps.PlanCode,
		Limits: LimitsCapsView{
			MaxAgents:        caps.MaxAgents,
			MaxDomains:       caps.MaxDomains,
			MaxMessagesMonth: caps.MaxMessagesMonth,
			MaxStorageBytes:  caps.MaxStorageBytes,
		},
		Usage:      usage,
		UpgradeURL: caps.UpgradeURL,
	}}, nil
}
