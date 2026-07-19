package sendramp_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/sendramp"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/migrations"
)

type permanentMarker interface{ Permanent() bool }

func TestReserveClassifiesInvariantErrorsAsPermanent(t *testing.T) {
	store, _, userID, _, messageID := seedRampMessage(t, "permanent-errors")
	for name, req := range map[string]sendramp.ReserveRequest{
		"invalid request": {},
		"missing domain":  {MessageID: messageID, UserID: userID, Domain: "missing.example.com", Units: 1, Day: time.Now(), Schedule: sendramp.DefaultSchedule},
	} {
		_, err := store.Reserve(context.Background(), req)
		var permanent permanentMarker
		if !errors.As(err, &permanent) || !permanent.Permanent() || errors.Unwrap(err) == nil {
			t.Errorf("%s error=%v, want wrapped permanent error", name, err)
		}
	}
}

func seedRampMessage(t *testing.T, suffix string) (*sendramp.Store, *pgxpool.Pool, string, string, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	userID, domain, messageID := seedRampMessageOnPool(t, pool, suffix)
	return sendramp.NewStore(pool), pool, userID, domain, messageID
}

func TestMigration067ExemptsOnlyPreexistingVerifiedDomains(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	ids := identity.NewStore(pool)
	user, err := ids.CreateOrGetUser(ctx, "migration-ramp@example.com", "Migration", "migration-ramp")
	if err != nil {
		t.Fatal(err)
	}
	verified, unverified := "preverified.example.net", "unverified.example.net"
	for _, domain := range []string{verified, unverified} {
		if _, err := ids.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET sending_status='verified' WHERE domain=$1`, verified); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.FS.ReadFile("067_domain_sending_ramp.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("execute migration 067: %v", err)
	}
	for domain, want := range map[string]string{verified: sendramp.StatusExempt, unverified: sendramp.StatusInactive} {
		var got string
		if err := tx.QueryRow(ctx, `SELECT sending_ramp_status FROM domains WHERE domain=$1`, domain).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s status=%q, want %q", domain, got, want)
		}
	}
}

func seedRampMessageOnPool(t *testing.T, pool *pgxpool.Pool, suffix string) (string, string, string) {
	t.Helper()
	ids := identity.NewStore(pool)
	ctx := context.Background()
	user, err := ids.CreateOrGetUser(ctx, "ramp-"+suffix+"@example.com", "Ramp", "ramp-"+suffix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := suffix + ".example.com"
	if _, err := ids.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET sending_status = 'verified' WHERE domain = $1`, domain); err != nil {
		t.Fatalf("verify sending domain: %v", err)
	}
	agent, err := ids.CreateAgent(ctx, "agent@"+domain, domain, "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	msg, err := ids.CreateOutboundMessage(ctx, agent.ID, []string{"one@example.net"}, nil, nil, "subject", "send", "smtp", "", "", []byte("raw"))
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	return user.ID, domain, msg.ID
}

func reserve(t *testing.T, store *sendramp.Store, userID, domain, messageID string, units int, day time.Time, schedule sendramp.Schedule) sendramp.Decision {
	t.Helper()
	d, err := store.Reserve(context.Background(), sendramp.ReserveRequest{
		MessageID: messageID,
		UserID:    userID,
		Domain:    domain,
		Units:     units,
		Day:       day,
		Schedule:  schedule,
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	return d
}

func TestReserveCountsRecipientUnitsAndIsMessageIdempotent(t *testing.T) {
	store, pool, userID, domain, messageID := seedRampMessage(t, "units")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 3)

	first := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !first.Allowed || first.UsedToday != 2 || first.DailyLimit != 50 {
		t.Fatalf("first decision = %+v, want allowed 2/50", first)
	}
	snapshot, err := store.Snapshot(context.Background(), userID, domain, day.Add(12*time.Hour))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Status != sendramp.StatusRamping || snapshot.UsedToday != 2 || snapshot.DailyLimit != 50 || snapshot.ActiveDays != 0 {
		t.Fatalf("snapshot = %+v, want ramping unqualified at 2/50", snapshot)
	}
	retry := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !retry.Allowed || retry.UsedToday != 2 {
		t.Fatalf("same-message retry = %+v, want idempotent 2/3", retry)
	}

	secondMessage := createMessageForAgent(t, pool, "agent@"+domain, "units-second")
	limited := reserve(t, store, userID, domain, secondMessage, 49, day, schedule)
	if limited.Allowed || limited.UsedToday != 2 || limited.DailyLimit != 50 {
		t.Fatalf("second decision = %+v, want limited at 2/50", limited)
	}
}

func TestReserveRetryAfterTransientPostReservationFailureIsIdempotent(t *testing.T) {
	store, _, userID, domain, messageID := seedRampMessage(t, "post-reserve-retry")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 3)

	first := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !first.Allowed || first.UsedToday != 2 {
		t.Fatalf("first reservation = %+v, want allowed at 2 used", first)
	}
	// A later pre-provider dependency can fail after Reserve succeeds. The retry
	// intentionally keeps and reuses the reservation rather than releasing it.
	retry := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !retry.Allowed || retry.UsedToday != 2 || retry.DailyLimit != first.DailyLimit {
		t.Fatalf("retry reservation = %+v, want idempotent reuse of %+v", retry, first)
	}
}

func TestReserveCrossDayMovesPendingCapacity(t *testing.T) {
	store, pool, userID, domain, messageID := seedRampMessage(t, "cross-day")
	day1 := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 2)
	if got := reserve(t, store, userID, domain, messageID, 7, day1, schedule); !got.Allowed {
		t.Fatalf("day-one reserve = %+v", got)
	}
	if got := reserve(t, store, userID, domain, messageID, 7, day1.AddDate(0, 0, 1), schedule); !got.Allowed {
		t.Fatalf("day-two move = %+v", got)
	}
	var oldReserved, newReserved, reservations int
	if err := pool.QueryRow(context.Background(), `SELECT reserved_count FROM domain_send_counters WHERE user_id=$1 AND domain=$2 AND day=$3`, userID, "example.com", day1).Scan(&oldReserved); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT reserved_count FROM domain_send_counters WHERE user_id=$1 AND domain=$2 AND day=$3`, userID, "example.com", day1.AddDate(0, 0, 1)).Scan(&newReserved); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sending_ramp_reservations WHERE message_id=$1`, messageID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if oldReserved != 0 || newReserved != 7 || reservations != 1 {
		t.Fatalf("old=%d new=%d rows=%d, want 0/7/1", oldReserved, newReserved, reservations)
	}
}

func TestConfirmQualifiesOnlyAtHalfAcceptedVolume(t *testing.T) {
	store, pool, userID, domain, firstMessage := seedRampMessage(t, "confirmed-volume")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 2)
	messages := []string{firstMessage}
	for i := 1; i < 25; i++ {
		messages = append(messages, createMessageForAgent(t, pool, "agent@"+domain, fmt.Sprintf("confirmed-%d", i)))
	}
	for i, messageID := range messages {
		if got := reserve(t, store, userID, domain, messageID, 1, day, schedule); !got.Allowed {
			t.Fatalf("reserve %d = %+v", i, got)
		}
		if err := store.Confirm(context.Background(), messageID); err != nil {
			t.Fatalf("Confirm(%d): %v", i, err)
		}
		var activeDays int
		if err := pool.QueryRow(context.Background(), `SELECT active_days FROM sending_ramp_scopes WHERE user_id=$1 AND domain=$2`, userID, "example.com").Scan(&activeDays); err != nil {
			t.Fatal(err)
		}
		want := 0
		if i == 24 {
			want = 1
		}
		if activeDays != want {
			t.Fatalf("after %d confirms active_days=%d, want %d", i+1, activeDays, want)
		}
	}
	if err := store.Confirm(context.Background(), messages[24]); err != nil {
		t.Fatalf("idempotent Confirm: %v", err)
	}
}

func TestReleaseReturnsPendingCapacityWithoutProgress(t *testing.T) {
	store, pool, userID, domain, messageID := seedRampMessage(t, "release")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	if got := reserve(t, store, userID, domain, messageID, 25, day, sendramp.NewSchedule(50, 100, 2)); !got.Allowed {
		t.Fatalf("reserve = %+v", got)
	}
	if err := store.Release(context.Background(), messageID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := store.Release(context.Background(), messageID); err != nil {
		t.Fatalf("idempotent Release: %v", err)
	}
	var reserved, confirmed, active int
	if err := pool.QueryRow(context.Background(), `SELECT reserved_count, confirmed_count FROM domain_send_counters WHERE user_id=$1 AND domain=$2 AND day=$3`, userID, "example.com", day).Scan(&reserved, &confirmed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT active_days FROM sending_ramp_scopes WHERE user_id=$1 AND domain=$2`, userID, "example.com").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 || confirmed != 0 || active != 0 {
		t.Fatalf("reserved=%d confirmed=%d active=%d, want zero", reserved, confirmed, active)
	}
}

func TestResolveUsesDurableMessageOutcome(t *testing.T) {
	store, pool, userID, domain, sentMessage := seedRampMessage(t, "terminal-outcome")
	failedMessage := createMessageForAgent(t, pool, "agent@"+domain, "resolve-failed")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{sentMessage, failedMessage} {
		if !reserve(t, store, userID, domain, id, 1, day, sendramp.DefaultSchedule).Allowed {
			t.Fatal("reserve denied")
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE messages SET delivery_status='sent' WHERE id=$1`, sentMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE messages SET delivery_status='failed' WHERE id=$1`, failedMessage); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{sentMessage, failedMessage} {
		if err := store.Resolve(context.Background(), id); err != nil {
			t.Fatalf("Resolve(%s): %v", id, err)
		}
	}
	var sentState, failedState string
	_ = pool.QueryRow(context.Background(), `SELECT state FROM sending_ramp_reservations WHERE message_id=$1`, sentMessage).Scan(&sentState)
	_ = pool.QueryRow(context.Background(), `SELECT state FROM sending_ramp_reservations WHERE message_id=$1`, failedMessage).Scan(&failedState)
	if sentState != "confirmed" || failedState != "released" {
		t.Fatalf("sent=%q failed=%q", sentState, failedState)
	}
}

func TestResolveReconfirmsReleasedReservationAfterProviderCorrection(t *testing.T) {
	store, pool, userID, domain, messageID := seedRampMessage(t, "resolve-provider-correction")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	if got := reserve(t, store, userID, domain, messageID, 10, day, sendramp.DefaultSchedule); !got.Allowed {
		t.Fatalf("reserve = %+v", got)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE messages SET delivery_status='failed' WHERE id=$1`, messageID); err != nil {
		t.Fatal(err)
	}
	if err := store.Resolve(context.Background(), messageID); err != nil {
		t.Fatalf("resolve local failure: %v", err)
	}

	// Provider feedback may authoritatively correct a locally inferred failure.
	// Reconciliation must restore both consumed and confirmed volume rather than
	// leaving the once-released send invisible to ramp accounting.
	if _, err := pool.Exec(context.Background(), `UPDATE messages SET delivery_status='delivered' WHERE id=$1`, messageID); err != nil {
		t.Fatal(err)
	}
	if err := store.Resolve(context.Background(), messageID); err != nil {
		t.Fatalf("resolve provider correction: %v", err)
	}
	var state string
	var reserved, confirmed int
	if err := pool.QueryRow(context.Background(),
		`SELECT r.state, c.reserved_count, c.confirmed_count
		   FROM sending_ramp_reservations r
		   JOIN domain_send_counters c ON c.user_id=r.user_id AND c.domain=r.domain AND c.day=r.day
		  WHERE r.message_id=$1`, messageID).Scan(&state, &reserved, &confirmed); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || reserved != 10 || confirmed != 10 {
		t.Fatalf("state=%q reserved=%d confirmed=%d, want confirmed/10/10", state, reserved, confirmed)
	}
}

func TestReserveProgressesOnlyOnActiveDaysAndPersistsCompletion(t *testing.T) {
	store, pool, userID, domain, firstMessage := seedRampMessage(t, "active-days")
	schedule := sendramp.NewSchedule(50, 100, 2)
	day1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got := reserve(t, store, userID, domain, firstMessage, 25, day1, schedule); got.DailyLimit != 50 {
		t.Fatalf("day 1 = %+v, want limit 50", got)
	}
	if err := store.Confirm(context.Background(), firstMessage); err != nil {
		t.Fatal(err)
	}

	// Ten idle calendar days do not advance the curve. The next active day is
	// active-day index 1, not calendar-day index 10.
	secondMessage := createMessageForAgent(t, pool, "agent@"+domain, "active-second")
	day11 := day1.AddDate(0, 0, 10)
	if got := reserve(t, store, userID, domain, secondMessage, 50, day11, schedule); !got.Allowed || got.DailyLimit != 100 {
		t.Fatalf("second qualified day = %+v, want allowed at target 100", got)
	}
	if err := store.Confirm(context.Background(), secondMessage); err != nil {
		t.Fatal(err)
	}

	thirdMessage := createMessageForAgent(t, pool, "agent@"+domain, "active-third")
	complete := reserve(t, store, userID, domain, thirdMessage, 50, day11.AddDate(0, 0, 1), schedule)
	if !complete.Allowed || complete.Status != sendramp.StatusComplete || complete.DailyLimit != 0 {
		t.Fatalf("post-ramp decision = %+v, want persisted complete and unlimited", complete)
	}
}

func TestReserveScopesOrganizationalDomainByTenant(t *testing.T) {
	pool := testutil.TestDB(t)
	store := sendramp.NewStore(pool)
	userA, domainA, messageA := seedRampMessageOnPool(t, pool, "a.mail")
	userB, domainB, messageB := seedRampMessageOnPool(t, pool, "b.mail")
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 2)

	if got := reserve(t, store, userA, domainA, messageA, 1, day, schedule); !got.Allowed {
		t.Fatalf("tenant A = %+v, want allowed", got)
	}
	if got := reserve(t, store, userB, domainB, messageB, 1, day, schedule); !got.Allowed {
		t.Fatalf("tenant B = %+v, want independent allowance", got)
	}
}

func TestReserveSiblingDomainsShareTenantRampProgress(t *testing.T) {
	pool := testutil.TestDB(t)
	ids := identity.NewStore(pool)
	ctx := context.Background()
	user, err := ids.CreateOrGetUser(ctx, "shared-ramp@example.com", "Shared", "shared-ramp")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	seed := func(prefix string) (string, string) {
		domain := prefix + ".shared.example.com"
		if _, err := ids.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
			t.Fatalf("ClaimOrCreateDomain: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE domains SET sending_status = 'verified' WHERE domain = $1`, domain); err != nil {
			t.Fatalf("verify domain: %v", err)
		}
		agent, err := ids.CreateAgent(ctx, "agent@"+domain, domain, "", "", "local", user.ID)
		if err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
		return domain, createMessageForAgent(t, pool, agent.ID, prefix+"-first")
	}
	domainA, messageA := seed("one")
	domainB, messageB := seed("two")
	store := sendramp.NewStore(pool)
	schedule := sendramp.NewSchedule(50, 100, 2)
	day1 := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)

	if got := reserve(t, store, user.ID, domainA, messageA, 50, day1, schedule); !got.Allowed {
		t.Fatalf("first sibling = %+v, want allowed", got)
	}
	if got := reserve(t, store, user.ID, domainB, messageB, 1, day1, schedule); got.Allowed {
		t.Fatalf("second sibling = %+v, want shared day-one pool exhausted", got)
	}

	messageB2 := createMessageForAgent(t, pool, "agent@"+domainB, "two-second")
	if err := store.Confirm(context.Background(), messageA); err != nil {
		t.Fatal(err)
	}
	if got := reserve(t, store, user.ID, domainB, messageB2, 50, day1.AddDate(0, 0, 1), schedule); !got.Allowed || got.DailyLimit != 100 {
		t.Fatalf("shared second active day = %+v, want target limit 100", got)
	}
}

func TestReserveConcurrentRecipientUnitsNeverExceedLimit(t *testing.T) {
	store, pool, userID, domain, firstMessage := seedRampMessage(t, "concurrent")
	messages := []string{firstMessage}
	for i := 1; i < 10; i++ {
		messages = append(messages, createMessageForAgent(t, pool, "agent@"+domain, fmt.Sprintf("concurrent-%d", i)))
	}
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(50, 100, 2)

	var wg sync.WaitGroup
	allowedUnits := make(chan int, len(messages))
	for _, messageID := range messages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := store.Reserve(context.Background(), sendramp.ReserveRequest{
				MessageID: messageID, UserID: userID, Domain: domain,
				Units: 6, Day: day, Schedule: schedule,
			})
			if err != nil {
				t.Errorf("Reserve(%s): %v", messageID, err)
				return
			}
			if d.Allowed {
				allowedUnits <- 6
			}
		}()
	}
	wg.Wait()
	close(allowedUnits)
	total := 0
	for units := range allowedUnits {
		total += units
	}
	if total != 48 {
		t.Fatalf("concurrent allowed units = %d, want 48 (largest multiple of 6 <= 50)", total)
	}
	snapshot, err := store.Snapshot(context.Background(), userID, domain, day)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.UsedToday != total || snapshot.UsedToday > snapshot.DailyLimit {
		t.Fatalf("snapshot = %+v, want used=%d and never above limit", snapshot, total)
	}
}

func TestExemptPersistsAcrossLaterRampEnable(t *testing.T) {
	store, _, userID, domain, messageID := seedRampMessage(t, "disabled-first")
	if err := store.Exempt(context.Background(), userID, domain); err != nil {
		t.Fatalf("Exempt: %v", err)
	}
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	d := reserve(t, store, userID, domain, messageID, 50, day, sendramp.NewSchedule(50, 2000, 30))
	if !d.Allowed || d.Status != sendramp.StatusExempt || d.DailyLimit != 0 {
		t.Fatalf("later enabled reservation = %+v, want permanently exempt", d)
	}
}

func createMessageForAgent(t *testing.T, pool *pgxpool.Pool, agentID, suffix string) string {
	t.Helper()
	id := "msg_ramp_" + suffix
	_, err := pool.Exec(context.Background(), `
		INSERT INTO messages (id, agent_id, direction, recipient, subject, message_type, method, conversation_id, to_recipients, cc, bcc, status, sender)
		VALUES ($1, $2, 'outbound', 'one@example.net', 'subject', 'send', 'smtp', '', ARRAY['one@example.net'], '{}', '{}', 'sent', $2)`, id, agentID)
	if err != nil {
		t.Fatalf("create message %s: %v", fmt.Sprint(suffix), err)
	}
	return id
}
