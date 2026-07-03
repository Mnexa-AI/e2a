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
	"log"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/httpapi"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/jackc/pgx/v5"
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

	// SESRegion is the SES sending-identity region (config
	// sender_identity.ses_region). Non-empty enables the sending feature, which
	// makes domainView emit the deterministic mail_from_* DNS records. Mirrors
	// the gate used to wire SenderIdentity below.
	SESRegion string

	// SigningSecret is the deployment HMAC secret (config.Signing.HMACSecret) —
	// used to mint/verify short-lived attachment download tokens (§6a #5), the
	// same primitive as the HITL magic-link. When empty, attachment endpoints
	// are left unwired.
	SigningSecret string

	// EventsEnabled mirrors WEBHOOKS_OUTBOX_ENABLED (the outbox's flag). When
	// false the webhook_events durable log is never written, so the events
	// list/get/redeliver endpoints return 501 events_log_disabled instead of an
	// empty result. Webhook delivery is unaffected.
	EventsEnabled bool

	// Legacy is the gorilla/mux handler the chi root falls back to for any
	// route not on /v1. WSHandle serves the /v1 WebSocket upgrade.
	Legacy   http.Handler
	WSHandle func(w http.ResponseWriter, r *http.Request, address string)

	// SenderIdentity (decision 4 / Slice 4) schedules SES sending-identity
	// provisioning on domain verify and teardown on domain delete. Optional —
	// nil when SES is not configured (dev/self-host), leaving sending_status
	// at none and the relay From in place. *senderidentity.Manager satisfies it.
	SenderIdentity SenderIdentityEnqueuer
}

// SenderIdentityEnqueuer is the slice of *senderidentity.Manager apiserver
// needs. Defined as an interface so apiserver does not hard-depend on the
// senderidentity package (River + AWS SDK) just to wire two optional deps.
type SenderIdentityEnqueuer interface {
	EnqueueProvision(ctx context.Context, domain string) error
	EnqueueDeprovisionTx(ctx context.Context, tx pgx.Tx, domain string) error
}

// BuildDeps maps Params into the httpapi dependency set. Kept as the single
// definition of the /v1 wiring so production and tests cannot diverge.
func BuildDeps(p Params) httpapi.Deps {
	return httpapi.Deps{
		Authenticator:          p.API.AuthenticateUser,
		PrincipalAuthenticator: p.API.AuthenticatePrincipal,
		AuthChallenge:          p.API.WWWAuthenticateChallenge,
		ListAgents:             p.Store.ListAgentsByUser,
		GetAgent:               p.Store.GetAgentByEmail,
		GetMessage:             p.Store.GetMessageWithContent,
		AttachmentStore:        attachmentStore(p),
		ListMessages:           p.Store.GetMessagesByAgent,
		ModifyMessageLabels:    p.Store.ModifyMessageLabels,

		ListConversations: p.Store.ListConversationsByAgent,
		GetConversation:   p.Store.GetConversationByID,

		CreateAgent:           p.Store.CreateAgent,
		LookupDomain:          p.Store.LookupDomain,
		EnforceAgentCreate:    p.Enforcer.CheckAgentCreate,
		UpdateAgentName:       p.Store.UpdateAgentName,
		UpdateAgentProtection: p.Store.UpdateAgentProtection,
		DeleteAgent:           p.Store.DeleteAgent,

		ListDomains:          p.Store.ListDomainsByUser,
		ClaimDomain:          p.Store.ClaimOrCreateDomain,
		EnforceDomainCreate:  p.Enforcer.CheckDomainCreate,
		DeleteDomain:         deleteDomainFunc(p),
		HasAgentsOnDomain:    p.Store.HasAgentsOnDomain,
		SMTPDomain:           p.SMTPDomain,
		SESRegion:            p.SESRegion,
		CursorSecret:         p.SigningSecret,
		EventsEnabled:        p.EventsEnabled,
		Idempotency:          p.Idempotency,
		DeliverOutbound:      p.API.DeliverOutbound,
		SendTest:             p.API.SendTestCore,
		ApprovePending:       p.API.ApprovePendingCore,
		SendLimit:            p.API.SendLimitAllow,
		PollLimit:            p.API.PollLimitAllow,
		RegLimit:             p.API.RegLimitAllow,
		DownloadLimit:        p.API.DownloadLimitAllow,
		RejectPending:        p.API.RejectPendingCore,
		GetReviewMessage:     p.Store.GetReviewMessage,
		ApproveInboundReview: p.API.ApproveInboundReviewCore,
		RejectInboundReview:  p.API.RejectInboundReviewCore,
		ListReviews:          p.Store.ListReviews,
		GetReviewWithContent: p.Store.GetReviewWithContent,
		EnforceMessageSend:   p.Enforcer.CheckMessageSend,
		GetRepliableMessage:  p.Store.GetRepliableMessage,
		GetLimits:            p.Enforcer.Get,
		ExportUserData:       p.API.ExportUserDataCore,
		DeleteUserData:       p.API.DeleteUserDataCore,
		ListSuppressions:     p.Store.ListSuppressions,
		RemoveSuppression:    p.Store.RemoveSuppression,
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

		CreateTemplate:     p.Store.CreateTemplate,
		ListTemplates:      p.Store.ListTemplatesByUser,
		GetTemplate:        p.Store.GetTemplateByID,
		GetTemplateByAlias: p.Store.GetTemplateByAlias,
		UpdateTemplate:     p.Store.UpdateTemplate,
		DeleteTemplate:     p.Store.DeleteTemplate,

		CreateScopedAPIKey: p.Store.CreateScopedAPIKey,
		ListAPIKeys:        p.Store.ListAPIKeys,
		DeleteAPIKey:       p.Store.DeleteAPIKey,

		TouchDomainChecked: p.Store.TouchDomainLastChecked,
		VerifyDomain:       p.Store.VerifyDomain,
		VerifyProbe: func(domain, token, dkimSel, dkimKey string) httpapi.DomainCheckResult {
			c := agent.CheckDomainRecords(domain, p.SMTPDomain, token, dkimSel, dkimKey, p.Production)
			return httpapi.DomainCheckResult{TXTFound: c.TXTFound, MX: c.MX, SPF: c.SPF, DKIM: c.DKIM}
		},
		EnqueueSenderProvision: enqueueSenderProvisionFunc(p),

		SharedDomain: p.SharedDomain,
		PublicURL:    p.PublicURL,
		WSHandle:     p.WSHandle,
		Legacy:       p.Legacy,
	}
}

// deleteDomainFunc wires DELETE /domains. With SES configured the domain-row
// delete and the SES deprovision job commit in ONE transaction (decision 4 —
// the teardown job can never be lost); without it, a plain delete.
func deleteDomainFunc(p Params) func(ctx context.Context, domain, userID string) error {
	if p.SenderIdentity == nil {
		return p.Store.DeleteDomain
	}
	return func(ctx context.Context, domain, userID string) error {
		return p.Store.DeleteDomainTx(ctx, domain, userID, func(ctx context.Context, tx pgx.Tx) error {
			return p.SenderIdentity.EnqueueDeprovisionTx(ctx, tx, domain)
		})
	}
}

// enqueueSenderProvisionFunc wires the verify-time provisioning hook. Nil when
// SES is not configured (the httpapi handler then no-ops). Best-effort: a
// failed enqueue is logged and recovered by the next POST /verify.
func enqueueSenderProvisionFunc(p Params) func(ctx context.Context, domain string) {
	if p.SenderIdentity == nil {
		return nil
	}
	return func(ctx context.Context, domain string) {
		if err := p.SenderIdentity.EnqueueProvision(ctx, domain); err != nil {
			log.Printf("[apiserver] enqueue sender provision for %s: %v", domain, err)
		}
	}
}

// attachmentStore wires the default (native) attachment store when the signing
// secret + public URL are present; returns nil otherwise (attachment endpoints
// stay unwired, e.g. in minimal test setups) — the handlers guard on nil.
func attachmentStore(p Params) httpapi.AttachmentStore {
	if p.SigningSecret == "" || p.PublicURL == "" {
		return nil
	}
	return httpapi.NewNativeAttachmentStore(p.SigningSecret, p.PublicURL)
}

// New builds the process HTTP handler (chi root owning /v1, legacy fallback).
func New(p Params) *httpapi.Server {
	return httpapi.New(BuildDeps(p))
}
