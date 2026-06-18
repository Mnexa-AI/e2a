package testutil

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/apiserver"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ContractServer struct {
	BaseURL    string
	APIKey     string
	UserID     string
	DBPool     *pgxpool.Pool
	Store      *identity.Store
	WSHub      *ws.Hub
	Signer     *headers.Signer
	SMTPAddr   string
	httpServer *http.Server
	httpLn     net.Listener
	smtpServer *relay.Server
}

func StartContractServer(ctx context.Context, dbURL string) (*ContractServer, error) {
	pool, err := OpenPreparedTestDB(ctx, dbURL)
	if err != nil {
		return nil, err
	}

	store := identity.NewStore(pool)
	signer := headers.NewSigner(TestHMACSecret)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()

	// Limits/usage/webhook components the /v1 Deps bind to. Caps are set
	// generously so contract scenarios exercise contract shape, not quota
	// enforcement (the 402 limit paths have dedicated httpapi unit tests).
	usageStore := usage.NewStore(pool)
	enforcer := limits.NewEnforcer(limits.NewStore(pool), usageStore, limits.Defaults{
		PlanCode: "contract_test", MaxAgents: 100000, MaxDomains: 100000,
		MaxMessagesMonth: 100000, MaxStorageBytes: 1 << 40,
	}, time.Minute)
	subscriberStore := webhook.NewSubscriberStore(pool)
	idempotencyStore := idempotency.NewStore(pool)

	router := mux.NewRouter()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotencyStore)
	api.SetEnforcer(enforcer)
	api.SetUsageStore(usageStore)
	api.SetSubscriberStore(subscriberStore)
	api.RegisterRoutes(router)

	wsHub := ws.NewHub()
	wsHandler := ws.NewHandler(wsHub, store)

	// Wrap the legacy mux with the typed /v1 surface using the SAME builder
	// the production binary uses, so contract scenarios hit the real /v1
	// handler (and a dep prod wires but the harness forgets fails loudly here).
	v1 := apiserver.New(apiserver.Params{
		API: api, Store: store, Enforcer: enforcer, UsageStore: usageStore,
		SubscriberStore: subscriberStore, Idempotency: idempotencyStore, Pool: pool,
		SMTPDomain: "test.e2a.dev", SharedDomain: "agents.e2a.dev",
		PublicURL: "http://127.0.0.1", Production: false,
		Legacy: router, WSHandle: wsHandler.ServeWithEmail,
	})

	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pool.Close()
		wsHub.Close()
		return nil, err
	}

	httpServer := &http.Server{
		Handler:           v1,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = httpServer.Serve(httpLn)
	}()

	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = httpServer.Shutdown(context.Background())
		_ = httpLn.Close()
		pool.Close()
		wsHub.Close()
		return nil, err
	}
	smtpAddr := smtpListener.Addr().String()
	_ = smtpListener.Close()

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			ListenAddr: smtpAddr,
			Domain:     "test.e2a.dev",
		},
		Env: "development",
	}
	smtpServer := relay.NewServer(cfg, store, signer, noopUsage, wsHub)
	go func() {
		_ = smtpServer.ListenAndServe()
	}()

	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", smtpAddr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	user, err := store.CreateOrGetUser(ctx, "contract@test.dev", "Contract Tester", "google-contract")
	if err != nil {
		_ = smtpServer.Close()
		_ = httpServer.Shutdown(context.Background())
		_ = httpLn.Close()
		wsHub.Close()
		pool.Close()
		return nil, err
	}
	key, err := store.CreateAPIKey(ctx, user.ID, "contract-key", nil)
	if err != nil {
		_ = smtpServer.Close()
		_ = httpServer.Shutdown(context.Background())
		_ = httpLn.Close()
		wsHub.Close()
		pool.Close()
		return nil, err
	}

	return &ContractServer{
		BaseURL:    "http://" + httpLn.Addr().String(),
		APIKey:     key.PlaintextKey,
		UserID:     user.ID,
		DBPool:     pool,
		Store:      store,
		WSHub:      wsHub,
		Signer:     signer,
		SMTPAddr:   smtpAddr,
		httpServer: httpServer,
		httpLn:     httpLn,
		smtpServer: smtpServer,
	}, nil
}

func (s *ContractServer) Close(ctx context.Context) error {
	var firstErr error
	if err := s.httpServer.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.httpLn.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.smtpServer.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	s.WSHub.Close()
	if err := truncateAll(ctx, s.DBPool); err != nil && firstErr == nil {
		firstErr = err
	}
	s.DBPool.Close()
	return firstErr
}
