package senderidentity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// defaultReaperInterval is how often the orphan-identity backstop sweeps.
// Hourly is plenty — the primary teardown is the transactional deprovision
// job; this only catches lost-job edge cases.
const defaultReaperInterval = time.Hour

// Migrate applies River's own schema (river_job et al.) to the pool. River
// tracks its migrations in its own river_migration table, separate from e2a's
// schema_migrations, so this is safe to run alongside identity.RunMigrations
// and is idempotent. Call once at startup, after the e2a migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	return nil
}

// Config tunes the Manager. Zero values get sane defaults.
type Config struct {
	// MaxReconcileAttempts bounds the pending→failed TTL. 0 → default.
	MaxReconcileAttempts int
	// ReaperInterval overrides the orphan-sweep cadence. 0 → default.
	ReaperInterval time.Duration
	// MaxWorkers is the default-queue concurrency. 0 → 5.
	MaxWorkers int
}

// Manager owns the River client and the sender-identity job lifecycle. It is
// the single integration point the rest of the app uses: EnqueueProvision on
// domain verify, EnqueueDeprovisionTx in the domain-delete tx, Start/Stop on
// the server lifecycle.
type Manager struct {
	client *river.Client[pgx.Tx]
}

// NewManager builds the River client with the provision/reconcile/deprovision
// workers + the periodic orphan reaper. The DB must already have River's
// schema (call Migrate first). fire may be nil (no events).
func NewManager(pool *pgxpool.Pool, store Store, provider Provider, fire EventFirer, cfg Config) (*Manager, error) {
	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	reaperInterval := cfg.ReaperInterval
	if reaperInterval <= 0 {
		reaperInterval = defaultReaperInterval
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &ProvisionWorker{store: store, provider: provider, fire: fire, maxReconcileAttempt: cfg.MaxReconcileAttempts})
	river.AddWorker(workers, &ReconcileWorker{store: store, provider: provider, fire: fire})
	river.AddWorker(workers, &DeprovisionWorker{provider: provider})
	river.AddWorker(workers, &ReapWorker{store: store, provider: provider})

	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(reaperInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				// No UniqueOpts: River's periodic scheduler already inserts at
				// most one per interval, and a completed reap must not dedup-
				// block the next scheduled run (River can't drop `completed`
				// from a unique state set). The reaper is idempotent anyway.
				return ReapArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers}},
		Workers:      workers,
		PeriodicJobs: periodic,
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Manager{client: client}, nil
}

// Start begins working jobs. Non-blocking.
func (m *Manager) Start(ctx context.Context) error { return m.client.Start(ctx) }

// Stop drains in-flight jobs and stops the client.
func (m *Manager) Stop(ctx context.Context) error { return m.client.Stop(ctx) }

// EnqueueProvision schedules sending-identity provisioning for a domain
// (called when a domain becomes verified, or on a forced re-check via
// POST /domains/{domain}/verify).
//
// Intentionally NOT unique: River's job uniqueness can't drop `completed` from
// its state set (only `retryable` is safely removable), so a completed job
// would block a legitimate re-provision — e.g. POST /verify retrying a `failed`
// domain — for the ~24h completed-job retention window. Instead we always
// enqueue and rely on the worker being idempotent: ProvisionWorker no-ops when
// the domain is already verified, and SES CreateEmailIdentity treats an
// existing identity as success. Concurrent duplicate enqueues are harmless.
func (m *Manager) EnqueueProvision(ctx context.Context, domain string) error {
	_, err := m.client.Insert(ctx, ProvisionArgs{Domain: domain}, nil)
	return err
}

// EnqueueDeprovisionTx enqueues sending-identity teardown WITHIN the caller's
// delete transaction, so the job is committed atomically with the domain-row
// delete — it can never be lost if SES is unreachable at delete time.
func (m *Manager) EnqueueDeprovisionTx(ctx context.Context, tx pgx.Tx, domain string) error {
	_, err := m.client.InsertTx(ctx, tx, DeprovisionArgs{Domain: domain}, nil)
	return err
}
