package sendramp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/publicsuffix"
)

const (
	StatusInactive = "inactive"
	StatusRamping  = "ramping"
	StatusComplete = "complete"
	StatusExempt   = "exempt"
)

type ReserveRequest struct {
	MessageID string
	UserID    string
	Domain    string
	Units     int
	Day       time.Time
	Schedule  Schedule
}

type Decision struct {
	Allowed    bool
	Status     string
	DailyLimit int
	UsedToday  int
	RetryAt    time.Time
}

// Snapshot is the read-only product view of a domain's durable ramp state.
type Snapshot struct {
	Status      string
	StartedAt   *time.Time
	CompletedAt *time.Time
	ActiveDays  int
	StartDaily  int
	TargetDaily int
	RampDays    int
	DailyLimit  int
	UsedToday   int
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Exempt records that a verified custom domain sent while enforcement was
// operator-disabled. Persisting the bypass prevents a later config enable from
// reclassifying an established sender as cold.
func (s *Store) Exempt(ctx context.Context, userID, domain string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE domains SET sending_ramp_status = 'exempt'
		 WHERE domain = $1 AND user_id = $2 AND sending_status = 'verified'
		   AND sending_ramp_status = 'inactive'`, domain, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM domains WHERE domain = $1 AND user_id = $2)`, domain, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("sendramp: domain owner mismatch")
		}
	}
	return nil
}

func (s *Store) Snapshot(ctx context.Context, userID, domain string, now time.Time) (Snapshot, error) {
	var snap Snapshot
	if err := s.pool.QueryRow(ctx, `SELECT sending_ramp_status FROM domains WHERE domain = $1 AND user_id = $2`, domain, userID).Scan(&snap.Status); err != nil {
		return Snapshot{}, err
	}
	if snap.Status != StatusRamping {
		return snap, nil
	}
	scope := registrableDomain(domain)
	var lastActiveDay *time.Time
	var scopeStatus string
	err := s.pool.QueryRow(ctx, `
		SELECT status, started_at, completed_at, active_days, last_active_day,
		       start_daily, target_daily, ramp_days
		  FROM sending_ramp_scopes WHERE user_id = $1 AND domain = $2`, userID, scope).Scan(
		&scopeStatus, &snap.StartedAt, &snap.CompletedAt, &snap.ActiveDays, &lastActiveDay,
		&snap.StartDaily, &snap.TargetDaily, &snap.RampDays)
	if err != nil {
		return Snapshot{}, err
	}
	if scopeStatus == StatusComplete {
		snap.Status = StatusComplete
		return snap, nil
	}
	day := utcDay(now)
	err = s.pool.QueryRow(ctx, `
		SELECT recipient_count, daily_limit FROM domain_send_counters
		 WHERE user_id = $1 AND domain = $2 AND day = $3`, userID, registrableDomain(domain), day).Scan(&snap.UsedToday, &snap.DailyLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		snap.DailyLimit = NewSchedule(snap.StartDaily, snap.TargetDaily, snap.RampDays).CapForActiveDay(snap.ActiveDays)
		return snap, nil
	}
	return snap, err
}

// Reserve atomically reserves recipient units before provider I/O. A
// (message, UTC day) reservation is idempotent, so River retries and crash
// recovery cannot consume the ramp more than once.
func (s *Store) Reserve(ctx context.Context, req ReserveRequest) (Decision, error) {
	if req.MessageID == "" || req.UserID == "" || req.Domain == "" || req.Units < 1 {
		return Decision{}, fmt.Errorf("sendramp: invalid reservation request")
	}
	day := utcDay(req.Day)
	schedule := NewSchedule(req.Schedule.StartDaily, req.Schedule.TargetDaily, req.Schedule.RampDays)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Decision{}, err
	}
	defer tx.Rollback(ctx)

	var owner, sendingStatus, domainStatus string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(user_id, ''), sending_status, sending_ramp_status
		  FROM domains WHERE domain = $1 FOR UPDATE`, req.Domain).Scan(&owner, &sendingStatus, &domainStatus)
	if err != nil {
		return Decision{}, err
	}
	if owner != req.UserID {
		return Decision{}, fmt.Errorf("sendramp: domain owner mismatch")
	}
	if domainStatus == StatusExempt || domainStatus == StatusComplete || sendingStatus != "verified" {
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: domainStatus})
	}

	scope := registrableDomain(req.Domain)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sending_ramp_scopes (user_id, domain, start_daily, target_daily, ramp_days)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, domain) DO NOTHING`,
		req.UserID, scope, schedule.StartDaily, schedule.TargetDaily, schedule.RampDays); err != nil {
		return Decision{}, err
	}
	var (
		scopeStatus                           string
		activeDays                            int
		lastActiveDay                         *time.Time
		storedStart, storedTarget, storedDays int
	)
	if err := tx.QueryRow(ctx, `
		SELECT status, active_days, last_active_day, start_daily, target_daily, ramp_days
		  FROM sending_ramp_scopes WHERE user_id = $1 AND domain = $2 FOR UPDATE`,
		req.UserID, scope).Scan(&scopeStatus, &activeDays, &lastActiveDay, &storedStart, &storedTarget, &storedDays); err != nil {
		return Decision{}, err
	}
	if domainStatus == StatusInactive {
		if _, err := tx.Exec(ctx, `UPDATE domains SET sending_ramp_status = 'ramping' WHERE domain = $1`, req.Domain); err != nil {
			return Decision{}, err
		}
	}
	schedule = NewSchedule(storedStart, storedTarget, storedDays)
	if scopeStatus == StatusComplete {
		if _, err := tx.Exec(ctx, `UPDATE domains SET sending_ramp_status = 'complete' WHERE domain = $1`, req.Domain); err != nil {
			return Decision{}, err
		}
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: StatusComplete})
	}
	if activeDays >= schedule.RampDays && (lastActiveDay == nil || utcDay(*lastActiveDay).Before(day)) {
		if _, err := tx.Exec(ctx, `UPDATE sending_ramp_scopes SET status = 'complete', completed_at = now() WHERE user_id = $1 AND domain = $2`, req.UserID, scope); err != nil {
			return Decision{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE domains SET sending_ramp_status = 'complete' WHERE domain = $1`, req.Domain); err != nil {
			return Decision{}, err
		}
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: StatusComplete})
	}

	var priorUnits int
	err = tx.QueryRow(ctx, `
		SELECT units FROM sending_ramp_reservations
		 WHERE message_id = $1 AND day = $2`, req.MessageID, day).Scan(&priorUnits)
	if err == nil {
		var used, limit int
		if err := tx.QueryRow(ctx, `SELECT recipient_count, daily_limit FROM domain_send_counters WHERE user_id = $1 AND domain = $2 AND day = $3`, req.UserID, scope, day).Scan(&used, &limit); err != nil {
			return Decision{}, err
		}
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: StatusRamping, DailyLimit: limit, UsedToday: used})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, err
	}

	limit := schedule.CapForActiveDay(activeDays)
	var used, appliedLimit int
	err = tx.QueryRow(ctx, `
		INSERT INTO domain_send_counters (user_id, domain, day, recipient_count, daily_limit)
		SELECT $1::text, $2::text, $3::date, $4::integer, $5::integer
		 WHERE $4::integer <= $5::integer
		ON CONFLICT (user_id, domain, day) DO UPDATE
		   SET recipient_count = domain_send_counters.recipient_count + EXCLUDED.recipient_count
		 WHERE domain_send_counters.recipient_count + EXCLUDED.recipient_count
		       <= domain_send_counters.daily_limit
		RETURNING recipient_count, daily_limit`, req.UserID, scope, day, req.Units, limit).Scan(&used, &appliedLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		err := tx.QueryRow(ctx, `SELECT recipient_count, daily_limit FROM domain_send_counters WHERE user_id = $1 AND domain = $2 AND day = $3`, req.UserID, scope, day).Scan(&used, &appliedLimit)
		if errors.Is(err, pgx.ErrNoRows) {
			used, appliedLimit = 0, limit
		} else if err != nil {
			return Decision{}, err
		}
		return commitDecision(ctx, tx, Decision{Allowed: false, Status: StatusRamping, DailyLimit: appliedLimit, UsedToday: used, RetryAt: day.Add(24 * time.Hour)})
	}
	if err != nil {
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sending_ramp_reservations (message_id, day, user_id, domain, units)
		VALUES ($1, $2, $3, $4, $5)`, req.MessageID, day, req.UserID, scope, req.Units); err != nil {
		return Decision{}, err
	}
	if lastActiveDay == nil || utcDay(*lastActiveDay).Before(day) {
		if _, err := tx.Exec(ctx, `
			UPDATE sending_ramp_scopes SET active_days = active_days + 1, last_active_day = $3
			 WHERE user_id = $1 AND domain = $2`, req.UserID, scope, day); err != nil {
			return Decision{}, err
		}
	}
	return commitDecision(ctx, tx, Decision{Allowed: true, Status: StatusRamping, DailyLimit: appliedLimit, UsedToday: used})
}

func commitDecision(ctx context.Context, tx pgx.Tx, d Decision) (Decision, error) {
	if err := tx.Commit(ctx); err != nil {
		return Decision{}, err
	}
	return d, nil
}

func utcDay(t time.Time) time.Time {
	if t.IsZero() {
		t = time.Now()
	}
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func registrableDomain(domain string) string {
	if d, err := publicsuffix.EffectiveTLDPlusOne(domain); err == nil && d != "" {
		return d
	}
	return domain
}
