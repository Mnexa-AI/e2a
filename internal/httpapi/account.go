package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// LimitsView mirrors the legacy LimitsInfo (GET /users/me/limits).
type LimitsView struct {
	PlanCode   string          `json:"plan_code"`
	Limits     LimitsCapsView  `json:"limits"`
	Usage      LimitsUsageView `json:"usage"`
	UpgradeURL string          `json:"upgrade_url"`
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

type limitsOutput struct{ Body LimitsView }

func (s *Server) registerAccount() {
	huma.Register(s.API, huma.Operation{
		OperationID: "getAccount", Method: http.MethodGet, Path: "/v1/account",
		Summary: "Get account: plan limits + usage", Tags: []string{"account"},
		Description: "The authenticated account's plan caps and current usage. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)",
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
}

type suppressionsOutput struct {
	Body struct {
		Suppressions []identity.Suppression `json:"suppressions"`
	}
}

func (s *Server) handleListSuppressions(ctx context.Context, _ *struct{}) (*suppressionsOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListSuppressions == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "suppressions are not available on this deployment")
	}
	list, err := s.deps.ListSuppressions(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list suppressions")
	}
	out := &suppressionsOutput{}
	out.Body.Suppressions = make([]identity.Suppression, 0, len(list))
	out.Body.Suppressions = append(out.Body.Suppressions, list...)
	return out, nil
}

type deleteSuppressionInput struct {
	Address string `path:"address"`
}
type deleteSuppressionOutput struct{ Status int }

func (s *Server) handleDeleteSuppression(ctx context.Context, in *deleteSuppressionInput) (*deleteSuppressionOutput, error) {
	user, err := s.requireUser(ctx)
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
	user, err := s.requireUser(ctx)
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
	user, err := s.requireUser(ctx)
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

func (s *Server) handleGetMyLimits(ctx context.Context, _ *struct{}) (*limitsOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
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
	return &limitsOutput{Body: LimitsView{
		PlanCode: caps.PlanCode,
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
