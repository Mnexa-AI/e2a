package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// Agent protection config sub-resource (design 2026-06-22-agent-protection-config.md).
//
// BETA. The screening posture (inbound/outbound gate + content scan + hold
// mechanism) lives here, not on AgentView, so the whole resource sits behind
// account scope: an agent-scoped credential — the entity being screened — can
// neither read its own detection tuning nor change its posture (closes audit
// #13/#21). The scan is exposed as a semantic sensitivity level; the engine's
// raw thresholds are derived server-side (see internal/identity/protection.go).

const protectionBetaDoc = "Beta: the agent protection config is unstable — its shape may change before it is declared stable."

// ProtectionGateView is one direction's trust gate: who may send, and what a
// non-match does. policy is a monotonic trust ladder (open < domain < allowlist).
// Leaves are optional (omitempty) with defaults, so a caller may send an empty
// gate/scan object and get the safe-permissive default. The three TOP-LEVEL
// keys (inbound/outbound/holds) stay required (design D3) — a missing section
// is a 422, not a silent reset.
//
// INBOUND gate identity note (#299): the inbound allowlist/domain gate matches on
// the message's From address, which only carries a specific per-agent identity for
// senders on a sending-verified domain. Mail from agents NOT yet sending-verified
// is relayed under the shared "via e2a" address (internal/outbound/sender.go) — it
// authenticates but is the same address for every such agent. The relay treats that
// shared sender as unresolvable (see senderResolvable), so it can never satisfy an
// allowlist/domain gate and is flagged (fail closed); under "open" it still passes.
// Net: per-agent inbound allowlisting is reliable for sending-verified senders;
// unverified intra-system senders are uniformly gated, not silently admitted.
//
// INBOUND authentication limitation (#318): the allowlist/domain gate matches the
// From address AS PRESENTED — it does not by itself require the From to be
// DMARC-aligned-authenticated. An external message that forges From to a listed
// address/domain but fails SPF/DKIM/DMARC can therefore satisfy the gate. The
// authentication verdict is delivered separately (the X-E2A-Auth-* headers /
// webhook auth fields), so a consumer that needs anti-spoofing must check it in
// addition to the gate. A DMARC-gated posture may return later as a composable,
// additive flag (the protection config is beta — its shape may still change).
type ProtectionGateView struct {
	Policy    string   `json:"policy,omitempty" enum:"open,allowlist,domain" default:"open" doc:"Trust gate: open (all), domain (listed domains), allowlist (listed addresses)."`
	Allowlist []string `json:"allowlist,omitempty" nullable:"false" maxItems:"1000" doc:"Addresses (allowlist) or domains (domain) the gate trusts; ignored for open. Inbound: matched against the message From AS PRESENTED — a match does not by itself prove the sender is authentic (a forged From that fails SPF/DKIM/DMARC can still match). For spoofing-sensitive trust, also check the message authentication result."`
	Action    string   `json:"action,omitempty" enum:"flag,review,block" default:"flag" doc:"What a gate non-match does: flag (deliver + annotate), review (hold), block."`
}

// ProtectionScanView is one direction's content scan, as a semantic sensitivity
// level. off disables it; low|medium|high tune how aggressively flagged content
// is held/blocked.
type ProtectionScanView struct {
	Sensitivity string `json:"sensitivity,omitempty" enum:"off,low,medium,high" default:"off" doc:"Content-scan sensitivity: off disables; low|medium|high increase aggressiveness."`
}

// ProtectionDirectionView pairs the gate and scan for one direction.
type ProtectionDirectionView struct {
	Gate ProtectionGateView `json:"gate"`
	Scan ProtectionScanView `json:"scan"`
}

// ProtectionHoldsView is the shared review-queue mechanism for held items.
type ProtectionHoldsView struct {
	TTLSeconds int    `json:"ttl_seconds,omitempty" minimum:"0" default:"604800" doc:"How long a held item waits before its on_expiry action fires."`
	OnExpiry   string `json:"on_expiry,omitempty" enum:"approve,reject" default:"reject" doc:"What happens to a held item when its TTL expires."`
}

// ProtectionConfigView is the GET response and the PUT body (full replace).
// The three top-level keys are required (no silent section reset); leaves are
// optional and fill from defaults.
type ProtectionConfigView struct {
	Inbound  ProtectionDirectionView `json:"inbound"`
	Outbound ProtectionDirectionView `json:"outbound"`
	Holds    ProtectionHoldsView     `json:"holds"`
}

func protectionViewFromIdentity(ag *identity.AgentIdentity) ProtectionConfigView {
	return ProtectionConfigView{
		Inbound: ProtectionDirectionView{
			Gate: ProtectionGateView{Policy: ag.InboundPolicy, Allowlist: orEmpty(ag.InboundAllowlist), Action: ag.InboundPolicyAction},
			Scan: ProtectionScanView{Sensitivity: ag.InboundScanSensitivity},
		},
		Outbound: ProtectionDirectionView{
			Gate: ProtectionGateView{Policy: ag.OutboundPolicy, Allowlist: orEmpty(ag.OutboundAllowlist), Action: ag.OutboundPolicyAction},
			Scan: ProtectionScanView{Sensitivity: ag.OutboundScanSensitivity},
		},
		Holds: ProtectionHoldsView{TTLSeconds: ag.HITLTTLSeconds, OnExpiry: ag.HITLExpirationAction},
	}
}

func protectionConfigFromView(v ProtectionConfigView) identity.ProtectionConfig {
	return identity.ProtectionConfig{
		InboundGatePolicy:       v.Inbound.Gate.Policy,
		InboundAllowlist:        v.Inbound.Gate.Allowlist,
		InboundGateAction:       v.Inbound.Gate.Action,
		InboundScanSensitivity:  v.Inbound.Scan.Sensitivity,
		OutboundGatePolicy:      v.Outbound.Gate.Policy,
		OutboundAllowlist:       v.Outbound.Gate.Allowlist,
		OutboundGateAction:      v.Outbound.Gate.Action,
		OutboundScanSensitivity: v.Outbound.Scan.Sensitivity,
		HITLTTLSeconds:          v.Holds.TTLSeconds,
		HITLExpirationAction:    v.Holds.OnExpiry,
	}
}

type getProtectionInput struct {
	Address string `path:"email" doc:"The agent's full email address."`
}

type protectionOutput struct {
	Body ProtectionConfigView
}

type putProtectionInput struct {
	Address string `path:"email" doc:"The agent's full email address."`
	Body    ProtectionConfigView
}

func (s *Server) registerAgentProtection() {
	huma.Register(s.API, huma.Operation{
		OperationID: "getAgentProtection",
		Method:      http.MethodGet,
		Path:        "/v1/agents/{email}/protection",
		Summary:     "Get an agent's protection config (beta)",
		Description: "Read the agent's protection posture — inbound/outbound trust gate, content-scan sensitivity, and hold-queue mechanism. Account scope only: an agent-scoped credential cannot read its own protection config. " + protectionBetaDoc,
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleGetProtection)

	huma.Register(s.API, huma.Operation{
		OperationID: "putAgentProtection",
		Method:      http.MethodPut,
		Path:        "/v1/agents/{email}/protection",
		Summary:     "Replace an agent's protection config (beta)",
		Description: "Replace the agent's protection posture wholesale. The three top-level keys (inbound, outbound, holds) are required; leaves default. Account scope only. " + protectionBetaDoc,
		Tags:        []string{"agents"},
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handlePutProtection)
}

func (s *Server) handleGetProtection(ctx context.Context, in *getProtectionInput) (*protectionOutput, error) {
	// Protection config is account administration — an agent-scoped credential
	// (the screened entity) must not read its own detection tuning (#13).
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	return &protectionOutput{Body: protectionViewFromIdentity(ag)}, nil
}

func (s *Server) handlePutProtection(ctx context.Context, in *putProtectionInput) (*protectionOutput, error) {
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	ag, err := s.resolveOwnedAgent(ctx, in.Address)
	if err != nil {
		return nil, err
	}
	if s.deps.UpdateAgentProtection == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "update unavailable")
	}
	cfg := protectionConfigFromView(in.Body)
	// Content scan is gated off by default for GA (see identity.ContentScanEnabled).
	// Clamp the scan knob to "off" rather than store a sensitivity that would
	// silently never run — so get_protection reads back the honest, effective
	// posture. Re-enabling the flag restores the caller's intended values on the
	// next write.
	if !identity.ContentScanEnabled() {
		cfg.InboundScanSensitivity = identity.SensitivityOff
		cfg.OutboundScanSensitivity = identity.SensitivityOff
	}
	if err := s.deps.UpdateAgentProtection(ctx, ag.ID, ag.UserID, cfg); err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	updated, err := s.deps.GetAgent(ctx, ag.ID)
	if err != nil || updated == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to reload agent")
	}
	return &protectionOutput{Body: protectionViewFromIdentity(updated)}, nil
}
