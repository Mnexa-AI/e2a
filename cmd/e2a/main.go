package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/apiserver"
	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/hitlworker"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/telemetry"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/Mnexa-AI/e2a/migrations"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// @title e2a API
// @version 1.0
// @description Email for AI agents. e2a delivers emails to your agent via webhooks or WebSocket and lets your agent send emails back.
// @description
// @description ## Authentication
// @description
// @description All requests require your API key as a Bearer token:
// @description
// @description ```
// @description Authorization: Bearer e2a_your_api_key
// @description ```
// @description
// @description Create an API key on the API Keys page of the e2a instance you are connecting to.
// @description
// @description ## How it works
// @description
// @description **Cloud agents** (webhook delivery):
// @description 1. Register your agent and set a webhook URL
// @description 2. When someone emails your agent, e2a POSTs a webhook to your endpoint
// @description 3. Reply via the API or send new emails
// @description
// @description **Local agents** (WebSocket delivery):
// @description 1. Register your agent with a slug on the deployment's shared domain (when slug registration is enabled)
// @description 2. Connect via WebSocket to receive real-time notifications
// @description 3. Fetch full message content via the API, reply or send new emails
// @contact.url https://github.com/Mnexa-AI/e2a
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description API key from the API Keys page (starts with `e2a_`). Format: `Bearer e2a_your_key`
func main() {
	// Load .env file if present (development convenience, not required)
	godotenv.Load()

	configPath := flag.String("config", "config.yaml", "path to config file")
	bootstrapEmail := flag.String("bootstrap-email", "", "create a user with this email and print a fresh API key, then exit (for self-host first-run)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Database
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Apply any pending schema migrations before anything else touches
	// the DB. E2A_MIGRATION_MODE controls behavior — auto (default)
	// applies pending; verify refuses to apply and errors if any are
	// pending; skip is a no-op for emergency surgery. See
	// internal/identity/migrate.go for the contract.
	migrationMode, err := identity.ParseMigrationMode(os.Getenv("E2A_MIGRATION_MODE"))
	if err != nil {
		log.Fatalf("Invalid E2A_MIGRATION_MODE: %v", err)
	}
	if err := identity.RunMigrations(ctx, pool, migrations.FS, migrationMode); err != nil {
		log.Fatalf("Schema migration failed: %v", err)
	}

	// Bootstrap mode: create a user + API key and exit. Used by self-host
	// operators to get their first key without needing Google OAuth.
	if *bootstrapEmail != "" {
		store := identity.NewStore(pool)
		user, err := store.BootstrapUser(ctx, *bootstrapEmail)
		if err != nil {
			log.Fatalf("Failed to bootstrap user: %v", err)
		}
		key, err := store.CreateAPIKey(ctx, user.ID, "bootstrap", nil)
		if err != nil {
			log.Fatalf("Failed to create API key: %v", err)
		}
		fmt.Printf("User:    %s (id=%s)\nAPI key: %s\n", user.Email, user.ID, key.PlaintextKey)
		fmt.Fprintln(os.Stderr, "save the API key now — it will not be shown again")
		return
	}

	log.Println("Connected to database")
	log.Printf("Config: env=%s domain=%s", cfg.Env, cfg.SMTP.Domain)

	// Services
	store := identity.NewStore(pool)
	if err := store.EnsureSharedDomain(ctx, cfg.SharedDomain); err != nil {
		log.Fatalf("Failed to seed shared domain row: %v", err)
	}
	signer := headers.NewSigner(cfg.Signing.HMACSecret)
	deliverer := webhook.NewDeliverer(cfg.IsProduction())
	deliveryStore := webhook.NewDeliveryStore(pool)
	persistentDeliverer := webhook.NewPersistentDeliverer(deliverer, deliveryStore)

	// New webhooks-as-a-resource pipeline (slice 1 of the feature).
	// Lives alongside the legacy single-URL path above — both fire on
	// inbound events. The feature flag E2A_FEATURE_WEBHOOK_RESOURCE
	// (default ON) gates the publisher only; the subscriber retry
	// worker keeps draining any pending rows even when the flag is
	// OFF. See tmp/e2a_webhooks_design.md for the full design.
	webhookFlag := webhookpub.StaticFlag(os.Getenv("E2A_FEATURE_WEBHOOK_RESOURCE") != "false")
	subscriberStore := webhook.NewSubscriberStore(pool)
	subscriberDeliverer := webhook.NewSubscriberDeliverer(cfg.IsProduction())
	webhookPublisher := webhookpub.New(store, webhookpub.NewDBInserter(pool), webhookFlag)

	// WEBHOOKS_OUTBOX_ENABLED controls the slice-1+slice-3 transactional
	// outbox path. Default off in v1 — see design §7.7. When off, the
	// outbox is wired but PublishTx is a no-op; the relay still wraps
	// the messages INSERT in a tx (one extra BEGIN/COMMIT overhead, no
	// outbox row). When on, the messages INSERT + webhook_events INSERT
	// commit together, closing the at-least-once publish-loss window.
	// Flip to true permanently in slice 11 after telemetry validates.
	//
	// Slice 2 (the worker that drains webhook_events) is not in this
	// commit; until it ships, enabling the flag in prod would let
	// events accumulate without delivery. Document for operators in
	// the runbook.
	outboxFlag := webhookpub.StaticFlag(os.Getenv("WEBHOOKS_OUTBOX_ENABLED") == "true")
	webhookOutbox := webhookpub.NewOutbox(pool, outboxFlag)
	// Slice 2: outbox publisher worker. Drains webhook_events into
	// webhook_subscriber_deliveries via LISTEN + 1s fallback poll. The
	// retry worker (existing) takes over from there. When the outbox
	// flag is off (default v1), webhook_events stays empty and this
	// worker has nothing to do — costs nothing to leave running.
	// Slice 10: telemetry backend. Log-based by default — operators
	// can swap to telemetry.NewPrometheus() (future) by changing this
	// one line. Every instrumented call site reads through this
	// interface so the switch is non-invasive.
	metrics := telemetry.NewLog()
	outboxWorker := webhookpub.NewOutboxWorker(pool, store).WithMetrics(metrics)
	smtpRelay := outbound.NewSMTPRelay(&cfg.OutboundSMTP)
	sender := outbound.NewSenderWithDKIM(smtpRelay, cfg.OutboundSMTP.FromDomain, store)

	// User auth (Google OAuth for agent developers)
	userAuth := auth.NewUserAuth(&cfg.OAuth, store, cfg.IsProduction())

	// Usage tracking is hosted-deployment infrastructure (counts every
	// inbound/outbound message into usage_events + usage_summaries for
	// downstream billing reconciliation). Self-hosters get the no-op
	// tracker by default — the writes are dead weight without an
	// external reader. Set E2A_USAGE_TRACKING=true to enable.
	var usageTracker usage.UsageTracker = usage.NewNoopUsageTracker()
	if os.Getenv("E2A_USAGE_TRACKING") == "true" {
		usageTracker = usage.NewUsageTracker(usage.NewStore(pool))
		log.Printf("Usage tracking enabled (writing to usage_events + usage_summaries)")
	}

	// HTTP API
	router := mux.NewRouter()
	api := agent.NewAPI(store, sender, smtpRelay, userAuth, usageTracker, cfg.SMTP.Domain, cfg.OutboundSMTP.FromDomain, cfg.SharedDomain, cfg.HTTP.PublicURL, cfg.IsProduction())
	// HITL magic-link token signer reuses the shared HMAC secret so operators
	// don't have to configure a second key.
	approvalSigner := approvaltoken.NewSigner(cfg.Signing.HMACSecret)
	api.SetApprovalSigner(approvalSigner)
	// HITL reviewer-notification emails. Requires both a configured
	// outbound SMTP relay (to actually send) and a public base URL (so
	// the magic links in the email are absolute and clickable from any
	// mail client). Without PublicURL, links would be relative and
	// unusable — safer to skip the notifier entirely and log a warning
	// so operators notice the misconfiguration.
	if cfg.OutboundSMTP.FromDomain == "" {
		log.Printf("[hitl] notifier disabled: outbound_smtp.from_domain is not set")
	} else if cfg.HTTP.PublicURL == "" {
		log.Printf("[hitl] notifier disabled: http.public_url is not set (magic links require an absolute base URL)")
	} else {
		api.SetNotifier(hitlnotify.New(store, smtpRelay, approvalSigner, cfg.OutboundSMTP.FromDomain, cfg.HTTP.PublicURL))
	}

	// OAuth 2.1 / fosite-backed authorization server. Needs the same
	// HMAC secret (signing.hmac_secret) for token HMAC signing and the
	// public URL as the canonical issuer. Without PublicURL, RFC 9207
	// `iss` emission + discovery would emit empty/inconsistent values
	// — skip wiring so /api/oauth/* return 404 and operators get a
	// loud signal that the deployment needs http.public_url set.
	var oauthStorage *oauth.Storage
	if cfg.HTTP.PublicURL == "" {
		log.Printf("[oauth] provider disabled: http.public_url is not set (required for issuer identity)")
	} else {
		oauthStorage = oauth.NewStorage(pool)
		oauthProvider, err := oauth.NewProvider(oauthStorage, cfg.HTTP.PublicURL, []byte(cfg.Signing.HMACSecret))
		if err != nil {
			log.Fatalf("[oauth] provider wiring failed: %v", err)
		}
		api.SetOAuthProvider(oauthProvider)
		// Consent handler also needs the storage pool for the cross-
		// package transaction (agent insert + auth-code insert atomic).
		api.SetOAuthStorage(oauthStorage)
		log.Printf("[oauth] provider enabled: issuer=%s", cfg.HTTP.PublicURL)
	}

	// Idempotency-Key support on /api/v1/send and /api/v1/agents/{email}/messages/{id}/reply.
	// Replays the cached response on retry; closes the double-send window for callers
	// behind at-least-once delivery (job queues, agent frameworks that retry tool calls,
	// model-driven re-invocations). Always wired in production — keeping it optional in
	// the agent package surfaces a clearer 5xx path for environments that don't run
	// against this codebase's postgres.
	idempotencyStore := idempotency.NewStore(pool)
	api.SetIdempotencyStore(idempotencyStore)

	// Resource-limits enforcer. The OSS server is plan-agnostic: it
	// reads the per-user caps from account_limits and enforces them at
	// agent-create / domain-register / message-send / inbound RCPT TO.
	// What "Free" or "Pro" mean (and how Stripe plumbs those into
	// account_limits) is the responsibility of an external provisioner
	// (hosted billing sidecar, admin tooling). Self-hosters who don't
	// run a provisioner get the generous config defaults applied to
	// every user — effectively unlimited unless they tighten the
	// `limits:` config block.
	usageStore := usage.NewStore(pool)
	enforcer := limits.NewEnforcer(
		limits.NewStore(pool),
		usageStore,
		limits.Defaults{
			PlanCode:         cfg.Limits.PlanCode,
			MaxAgents:        cfg.Limits.MaxAgents,
			MaxDomains:       cfg.Limits.MaxDomains,
			MaxMessagesMonth: cfg.Limits.MaxMessagesMonth,
			MaxStorageBytes:  cfg.Limits.MaxStorageBytes,
		},
		time.Duration(cfg.Limits.CacheTTLSeconds)*time.Second,
	)
	api.SetEnforcer(enforcer)
	api.SetUsageStore(usageStore)
	api.SetInternalAPISecret(cfg.Limits.InternalAPISecret)
	api.SetBillingHookURL(cfg.Limits.BillingHookURL)
	api.SetSubscriberStore(subscriberStore)
	api.SetPublisher(webhookPublisher)
	api.SetOutbox(webhookOutbox)
	// Slices 6 + 7: customer-facing events API needs the raw pool to
	// query webhook_events and write webhook_subscriber_deliveries on
	// replay. Kept as a separate setter so a future refactor can route
	// through a higher-level abstraction.
	api.SetPoolForEvents(pool)
	api.SetMetrics(metrics)

	api.RegisterRoutes(router)

	// WebSocket route for local-mode agents
	wsHub := ws.NewHub()
	defer wsHub.Close()
	wsHandler := ws.NewHandler(wsHub, store)
	api.RegisterWSRoute(router, wsHandler.Handle)

	// v1 contract layer (api-v1-redesign Slice 1). The new chi + Huma surface
	// owns the `/v1` prefix (OpenAPI-as-source-of-truth, standardized error
	// envelope + cursor pagination + X-Request-Id), and falls back to the
	// legacy gorilla/mux handlers for every route not yet ported (the
	// strangler). Legacy `/api/v1` routes are deleted as each resource lands
	// on Huma, within this same PR. The chi root is the process HTTP handler.
	v1 := apiserver.New(apiserver.Params{
		API:             api,
		Store:           store,
		Enforcer:        enforcer,
		UsageStore:      usageStore,
		SubscriberStore: subscriberStore,
		Idempotency:     idempotencyStore,
		Pool:            pool,
		SMTPDomain:      cfg.SMTP.Domain,
		SharedDomain:    cfg.SharedDomain,
		PublicURL:       cfg.HTTP.PublicURL,
		Production:      cfg.IsProduction(),
		Legacy:          router,
		WSHandle:        wsHandler.ServeWithEmail,
	})

	httpServer := &http.Server{
		Addr:    cfg.HTTP.ListenAddr,
		Handler: v1,
	}

	// SMTP Relay
	smtpServer := relay.NewServer(cfg, store, signer, persistentDeliverer, usageTracker, wsHub)
	smtpServer.SetEnforcer(enforcer)
	smtpServer.SetPublisher(webhookPublisher)
	smtpServer.SetOutbox(webhookOutbox)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("HTTP API listening on %s", cfg.HTTP.ListenAddr)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	go func() {
		if err := smtpServer.ListenAndServe(); err != nil {
			log.Fatalf("SMTP server error: %v", err)
		}
	}()

	// workerWG tracks every background goroutine that needs to drain
	// before the process exits on SIGTERM. Without it the main
	// goroutine would return as soon as httpServer.Shutdown returns,
	// dropping in-flight webhook deliveries and HITL TTL transitions
	// mid-iteration. retryCancel/hitlCancel signal the workers to
	// stop; wg.Wait() at the end of shutdown blocks for the current
	// iteration to settle.
	//
	// Known gap: NotifyPendingApprovalAsync goroutines are detached
	// (context.Background, no handle) and remain at risk. Operators
	// running rolling deploys should ensure SMTP is reachable from
	// the new replica before reaping the old one so notifications
	// have somewhere to land. Threading the wg through the notifier
	// is a follow-up.
	var workerWG sync.WaitGroup

	// Webhook delivery retry worker (legacy single-URL path)
	retryWorker := webhook.NewRetryWorker(deliveryStore, deliverer, store)
	retryCtx, retryCancel := context.WithCancel(context.Background())
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		retryWorker.Start(retryCtx)
	}()

	// Webhook subscriber retry worker (new webhooks-as-a-resource path).
	// Lives alongside the legacy worker; both drain their own table.
	// Unconditionally started — even with the feature flag OFF (publisher
	// disabled) this worker keeps draining any rows that were inserted
	// before the flag flipped, so flipping the flag mid-flight doesn't
	// leak pending deliveries.
	subRetryWorker := webhook.NewSubscriberRetryWorker(subscriberStore, subscriberDeliverer, store)
	subRetryCtx, subRetryCancel := context.WithCancel(context.Background())
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		subRetryWorker.Start(subRetryCtx)
	}()

	// Auto-disable + signing-secret-grace janitor (slice 4). Same
	// lifecycle as the retry worker — share the same cancel so a
	// single shutdown signal stops both.
	autoDisableWorker := webhook.NewAutoDisableWorker(store)
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		autoDisableWorker.Start(subRetryCtx)
	}()

	// Slice 2: outbox publisher worker. Shares subRetryCtx so a
	// single shutdown signal stops the whole webhook-delivery stack.
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		outboxWorker.Start(subRetryCtx)
	}()

	// HITL expiration worker: transitions pending_approval messages that
	// blew past their TTL into expired_approved (auto-send) or
	// expired_rejected based on the owning agent's hitl_expiration_action.
	hitlWorker := hitlworker.New(store, sender, usageTracker, cfg.OutboundSMTP.FromDomain)
	hitlCtx, hitlCancel := context.WithCancel(context.Background())
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		if err := hitlWorker.Run(hitlCtx); err != nil && err != context.Canceled {
			log.Printf("[hitl-worker] stopped: %v", err)
		}
	}()

	// Periodic cleanup of expired messages and sessions. Bound to its
	// own cancel-context so shutdown stops the loop instead of
	// orphaning the goroutine.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				if deleted, err := store.DeleteExpiredMessages(cleanupCtx); err != nil {
					log.Printf("Failed to clean up expired messages: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired message(s)", deleted)
					metrics.JanitorRowsDeleted("messages", int(deleted))
				}

				if deleted, err := store.DeleteExpiredUserSessions(cleanupCtx); err != nil {
					log.Printf("Failed to clean up expired user sessions: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired user session(s)", deleted)
				}

				if deleted, err := deliveryStore.DeleteExpiredDeliveries(cleanupCtx); err != nil {
					log.Printf("Failed to clean up expired webhook deliveries: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired webhook delivery record(s)", deleted)
				}

				if deleted, err := subscriberStore.DeleteExpiredSubscriberDeliveries(cleanupCtx); err != nil {
					log.Printf("Failed to clean up expired webhook subscriber deliveries: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired webhook subscriber delivery record(s)", deleted)
					metrics.JanitorRowsDeleted("webhook_subscriber_deliveries", deleted)
				}

				// Slice 1 prerequisite janitor: webhook_events rows
				// also have a 30-day TTL (migration 026). Without
				// this, the table grows monotonically once the
				// outbox path starts writing events.
				if deleted, err := webhookOutbox.DeleteExpiredWebhookEvents(cleanupCtx); err != nil {
					log.Printf("Failed to clean up expired webhook events: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired webhook event(s)", deleted)
					metrics.JanitorRowsDeleted("webhook_events", deleted)
				}

				if oauthStorage != nil {
					if res, err := oauthStorage.CleanupExpired(cleanupCtx, time.Now()); err != nil {
						log.Printf("Failed to clean up expired OAuth rows: %v", err)
					} else if res.Total() > 0 {
						log.Printf("Cleaned up OAuth rows: codes=%d pkce=%d access=%d refresh=%d clients=%d",
							res.AuthCodesDeleted, res.PKCERequestsDeleted,
							res.AccessTokensDeleted, res.RefreshTokensDeleted,
							res.ClientsDeleted)
					}
				}
			}

			if deleted, err := idempotencyStore.Sweep(context.Background()); err != nil {
				log.Printf("Failed to sweep idempotency keys: %v", err)
			} else if deleted > 0 {
				log.Printf("Swept %d idempotency key(s) past TTL", deleted)
			}
		}
	}()

	<-sigCh
	log.Println("Shutting down...")

	// Signal every background worker to stop. Their inner ctx-select
	// branches return on the next iteration; processBatch / RunOnce
	// calls already in flight finish their current row before the
	// goroutine exits.
	retryCancel()
	subRetryCancel()
	hitlCancel()
	cleanupCancel()

	// SMTP server: close the listener so no new connections, but
	// existing connections finish their DATA per the relay's own
	// connection lifecycle.
	smtpServer.Close()

	// Single shared deadline for both HTTP shutdown and worker drain.
	// 30s matches Kubernetes' default terminationGracePeriodSeconds.
	// A naive `httpServer.Shutdown(30s)` followed by a second 30s
	// `workerWG.Wait()` would budget 60s total — past the platform's
	// SIGKILL window, the kernel reaps us before the drain phase even
	// runs. Sharing one deadline guarantees we don't outlast the
	// platform grace period, with whichever phase finishes first
	// donating the remainder to the other.
	//
	// Operators wanting longer drain (e.g. SMTP send to slow recipient
	// mid-flight) should bump terminationGracePeriodSeconds AND the
	// constant below in lockstep.
	const shutdownBudget = 30 * time.Second
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Wait for workers' current iteration to settle, bounded by the
	// REMAINING share of the same deadline (whatever Shutdown didn't
	// consume). Past it, fall through and let the goroutines die
	// with the process.
	drainDone := make(chan struct{})
	go func() {
		workerWG.Wait()
		close(drainDone)
	}()
	select {
	case <-drainDone:
		log.Println("Background workers drained cleanly.")
	case <-shutdownCtx.Done():
		log.Println("Background workers did not drain within shutdown budget; exiting anyway.")
	}
}
