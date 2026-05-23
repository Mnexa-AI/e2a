package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/hitlworker"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
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
		key, err := store.CreateAPIKey(ctx, user.ID, "bootstrap")
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
	smtpRelay := outbound.NewSMTPRelay(&cfg.OutboundSMTP)
	sender := outbound.NewSender(smtpRelay, cfg.OutboundSMTP.FromDomain)

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

	api.RegisterRoutes(router)

	// WebSocket route for local-mode agents
	wsHub := ws.NewHub()
	defer wsHub.Close()
	wsHandler := ws.NewHandler(wsHub, store)
	api.RegisterWSRoute(router, wsHandler.Handle)

	httpServer := &http.Server{
		Addr:    cfg.HTTP.ListenAddr,
		Handler: router,
	}

	// SMTP Relay
	smtpServer := relay.NewServer(cfg, store, signer, persistentDeliverer, usageTracker, wsHub)

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

	// Webhook delivery retry worker
	retryWorker := webhook.NewRetryWorker(deliveryStore, deliverer, store)
	retryCtx, retryCancel := context.WithCancel(context.Background())
	go retryWorker.Start(retryCtx)

	// HITL expiration worker: transitions pending_approval messages that
	// blew past their TTL into expired_approved (auto-send) or
	// expired_rejected based on the owning agent's hitl_expiration_action.
	hitlWorker := hitlworker.New(store, sender, usageTracker, cfg.OutboundSMTP.FromDomain)
	hitlCtx, hitlCancel := context.WithCancel(context.Background())
	go func() {
		if err := hitlWorker.Run(hitlCtx); err != nil && err != context.Canceled {
			log.Printf("[hitl-worker] stopped: %v", err)
		}
	}()

	// Periodic cleanup of expired messages and sessions
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if deleted, err := store.DeleteExpiredMessages(context.Background()); err != nil {
				log.Printf("Failed to clean up expired messages: %v", err)
			} else if deleted > 0 {
				log.Printf("Cleaned up %d expired message(s)", deleted)
			}

			if deleted, err := store.DeleteExpiredUserSessions(context.Background()); err != nil {
				log.Printf("Failed to clean up expired user sessions: %v", err)
			} else if deleted > 0 {
				log.Printf("Cleaned up %d expired user session(s)", deleted)
			}

			if deleted, err := deliveryStore.DeleteExpiredDeliveries(context.Background()); err != nil {
				log.Printf("Failed to clean up expired webhook deliveries: %v", err)
			} else if deleted > 0 {
				log.Printf("Cleaned up %d expired webhook delivery record(s)", deleted)
			}

			if oauthStorage != nil {
				if res, err := oauthStorage.CleanupExpired(context.Background(), time.Now()); err != nil {
					log.Printf("Failed to clean up expired OAuth rows: %v", err)
				} else if res.Total() > 0 {
					log.Printf("Cleaned up OAuth rows: codes=%d pkce=%d access=%d refresh=%d clients=%d",
						res.AuthCodesDeleted, res.PKCERequestsDeleted,
						res.AccessTokensDeleted, res.RefreshTokensDeleted,
						res.ClientsDeleted)
				}
			}
		}
	}()

	<-sigCh
	log.Println("Shutting down...")

	retryCancel()
	hitlCancel()
	smtpServer.Close()
	httpServer.Shutdown(ctx)
}
