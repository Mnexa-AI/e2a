// Package apiserver builds the process HTTP handler: the typed Huma /v1
// surface (internal/httpapi) wrapping the legacy gorilla/mux fallback.
//
// It exists so the production binary (cmd/e2a) and the contract-test harness
// (internal/testutil) construct the SAME handler from the SAME dependency
// wiring. Before this, the contract harness served only the legacy mux, so it
// could not exercise /v1 at all and would silently drift from production. With
// one builder, a dep that production wires but the harness forgets shows up as
// a failing contract test, not a silent gap.
package apiserver

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/httpapi"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Params bundles the already-constructed components the /v1 Deps closures bind
// to. Callers build these once (the binary for real, the harness for tests)
// and hand them over; the builder owns only the mapping into httpapi.Deps.
type Params struct {
	API             *agent.API
	Store           *identity.Store
	Enforcer        *limits.DBEnforcer
	UsageStore      *usage.Store
	SubscriberStore *webhook.SubscriberStore
	Idempotency     *idempotency.Store
	Pool            *pgxpool.Pool

	SMTPDomain   string
	SharedDomain string
	PublicURL    string
	Production   bool

	// Legacy is the gorilla/mux handler the chi root falls back to for any
	// route not on /v1. WSHandle serves the /v1 WebSocket upgrade.
	Legacy   http.Handler
	WSHandle func(w http.ResponseWriter, r *http.Request, address string)
}

// BuildDeps maps Params into the httpapi dependency set. Kept as the single
// definition of the /v1 wiring so production and tests cannot diverge.
func BuildDeps(p Params) httpapi.Deps {
	return httpapi.Deps{
		Authenticator: p.API.AuthenticateUser,
		ListAgents:    p.Store.ListAgentsByUser,
		GetAgent:      p.Store.GetAgentByEmail,
		GetMessage:    p.Store.GetMessageWithContent,
		ListMessages:  p.Store.GetMessagesByAgent,

		ListConversations: p.Store.ListConversationsByAgent,
		GetConversation:   p.Store.GetConversationByID,

		CreateAgent:        p.Store.CreateAgent,
		LookupDomain:       p.Store.LookupDomain,
		EnforceAgentCreate: p.Enforcer.CheckAgentCreate,
		UpdateAgentHITL:    p.Store.UpdateAgentHITL,
		DeleteAgent:        p.Store.DeleteAgent,

		ListDomains:         p.Store.ListDomainsByUser,
		ClaimDomain:         p.Store.ClaimOrCreateDomain,
		EnforceDomainCreate: p.Enforcer.CheckDomainCreate,
		SetDomainPrimary:    p.Store.SetDomainPrimary,
		DeleteDomain:        p.Store.DeleteDomain,
		HasAgentsOnDomain:   p.Store.HasAgentsOnDomain,
		SMTPDomain:          p.SMTPDomain,
		Idempotency:         p.Idempotency,
		DeliverOutbound:     p.API.DeliverOutbound,
		SendTest:            p.API.SendTestCore,
		ApprovePending:      p.API.ApprovePendingCore,
		SendLimit:           p.API.SendLimitAllow,
		PollLimit:           p.API.PollLimitAllow,
		RegLimit:            p.API.RegLimitAllow,
		RejectPending:       p.API.RejectPendingCore,
		EnforceMessageSend:  p.Enforcer.CheckMessageSend,
		GetInboundMessage:   p.Store.GetInboundMessage,
		GetLimits:           p.Enforcer.Get,
		ExportUserData:      p.API.ExportUserDataCore,
		DeleteUserData:      p.API.DeleteUserDataCore,
		GetUsage: func(ctx context.Context, userID string) httpapi.LimitsUsageView {
			var u httpapi.LimitsUsageView
			if n, err := p.UsageStore.CountAgentsByUser(ctx, userID); err == nil {
				u.Agents = n
			}
			if n, err := p.UsageStore.CountDomainsByUser(ctx, userID); err == nil {
				u.Domains = n
			}
			if n, err := p.UsageStore.MessagesThisMonth(ctx, userID); err == nil {
				u.MessagesMonth = n
			}
			if n, err := p.UsageStore.GetStorageBytes(ctx, userID); err == nil {
				u.StorageBytes = n
			}
			return u
		},

		ListEvents: func(ctx context.Context, q httpapi.EventQuery) ([]agent.EventJSON, error) {
			return agent.ListEventsForUser(ctx, p.Pool, q.UserID, q.Type, q.AgentID, q.ConversationID, q.MessageID, q.Since, q.Until, q.CursorCreatedAt, q.CursorID, q.Limit)
		},
		GetEvent2: func(ctx context.Context, userID, eventID string) (*agent.EventJSON, error) {
			return agent.GetEventForUser(ctx, p.Pool, userID, eventID)
		},
		LoadReplayEvent: func(ctx context.Context, userID, eventID string) (*agent.ReplayEvent, error) {
			return agent.LoadReplayEvent(ctx, p.Pool, userID, eventID)
		},
		InsertReplayDelivery: func(ctx context.Context, eventID, webhookID, eventType string, messageID *string, envelope []byte) (string, error) {
			return agent.InsertReplayDelivery(ctx, p.Pool, eventID, webhookID, eventType, messageID, envelope)
		},

		CreateWebhook:     p.Store.CreateWebhook,
		ListWebhooks:      p.Store.ListWebhooksByUser,
		GetWebhook:        p.Store.GetWebhookByID,
		UpdateWebhook:     p.Store.UpdateWebhook,
		DeleteWebhook:     p.Store.DeleteWebhook,
		RotateSecret:      p.Store.RotateSecret,
		TestWebhookInsert: p.SubscriberStore.InsertPendingForTest,
		ListDeliveries:    p.SubscriberStore.ListDeliveriesByWebhook,

		TouchDomainChecked: p.Store.TouchDomainLastChecked,
		VerifyDomain:       p.Store.VerifyDomain,
		VerifyProbe: func(domain, token, dkimSel, dkimKey string) httpapi.DomainCheckResult {
			c := agent.CheckDomainRecords(domain, p.SMTPDomain, token, dkimSel, dkimKey, p.Production)
			return httpapi.DomainCheckResult{TXTFound: c.TXTFound, MX: c.MX, SPF: c.SPF, DKIM: c.DKIM}
		},

		SharedDomain: p.SharedDomain,
		PublicURL:    p.PublicURL,
		WSHandle:     p.WSHandle,
		Legacy:       p.Legacy,
	}
}

// New builds the process HTTP handler (chi root owning /v1, legacy fallback).
func New(p Params) *httpapi.Server {
	return httpapi.New(BuildDeps(p))
}
