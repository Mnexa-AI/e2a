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

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

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

	var (
		owner, sendingStatus, status          string
		startedAt, completedAt                *time.Time
		activeDays                            int
		lastActiveDay                         *time.Time
		storedStart, storedTarget, storedDays int
	)
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(user_id, ''), sending_status, sending_ramp_status,
		       sending_ramp_started_at, sending_ramp_completed_at,
		       sending_ramp_active_days, sending_ramp_last_active_day,
		       sending_ramp_start_daily, sending_ramp_target_daily, sending_ramp_days
		  FROM domains WHERE domain = $1 FOR UPDATE`, req.Domain).Scan(
		&owner, &sendingStatus, &status, &startedAt, &completedAt,
		&activeDays, &lastActiveDay, &storedStart, &storedTarget, &storedDays)
	if err != nil {
		return Decision{}, err
	}
	if owner != req.UserID {
		return Decision{}, fmt.Errorf("sendramp: domain owner mismatch")
	}
	if status == StatusExempt || status == StatusComplete || sendingStatus != "verified" {
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: status})
	}
	if status == StatusInactive {
		status = StatusRamping
		storedStart, storedTarget, storedDays = schedule.StartDaily, schedule.TargetDaily, schedule.RampDays
		if _, err := tx.Exec(ctx, `
			UPDATE domains SET sending_ramp_status = 'ramping', sending_ramp_started_at = now(),
			       sending_ramp_start_daily = $2, sending_ramp_target_daily = $3,
			       sending_ramp_days = $4
			 WHERE domain = $1`, req.Domain, storedStart, storedTarget, storedDays); err != nil {
			return Decision{}, err
		}
	}
	schedule = NewSchedule(storedStart, storedTarget, storedDays)
	if activeDays >= schedule.RampDays && (lastActiveDay == nil || utcDay(*lastActiveDay).Before(day)) {
		if _, err := tx.Exec(ctx, `UPDATE domains SET sending_ramp_status = 'complete', sending_ramp_completed_at = now() WHERE domain = $1`, req.Domain); err != nil {
			return Decision{}, err
		}
		return commitDecision(ctx, tx, Decision{Allowed: true, Status: StatusComplete})
	}

	scope := registrableDomain(req.Domain)
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
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, domain, day) DO UPDATE
		   SET recipient_count = domain_send_counters.recipient_count + EXCLUDED.recipient_count,
		       daily_limit = LEAST(domain_send_counters.daily_limit, EXCLUDED.daily_limit)
		 WHERE domain_send_counters.recipient_count + EXCLUDED.recipient_count
		       <= LEAST(domain_send_counters.daily_limit, EXCLUDED.daily_limit)
		RETURNING recipient_count, daily_limit`, req.UserID, scope, day, req.Units, limit).Scan(&used, &appliedLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT recipient_count, daily_limit FROM domain_send_counters WHERE user_id = $1 AND domain = $2 AND day = $3`, req.UserID, scope, day).Scan(&used, &appliedLimit); err != nil {
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
			UPDATE domains SET sending_ramp_active_days = sending_ramp_active_days + 1,
			       sending_ramp_last_active_day = $2 WHERE domain = $1`, req.Domain, day); err != nil {
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
