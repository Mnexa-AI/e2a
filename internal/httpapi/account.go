package httpapi

import (
	"context"
	"net/http"

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
		OperationID: "getMyLimits", Method: http.MethodGet, Path: "/v1/users/me/limits",
		Summary: "Get plan limits + usage", Tags: []string{"account"},
		Description: "The authenticated account's plan caps and current usage.",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleGetMyLimits)
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
