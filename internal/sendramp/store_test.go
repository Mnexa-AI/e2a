package sendramp_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/sendramp"
	"github.com/tokencanopy/e2a/internal/testutil"
)

func seedRampMessage(t *testing.T, suffix string) (*sendramp.Store, *pgxpool.Pool, string, string, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	userID, domain, messageID := seedRampMessageOnPool(t, pool, suffix)
	return sendramp.NewStore(pool), pool, userID, domain, messageID
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
	schedule := sendramp.NewSchedule(3, 10, 3)

	first := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !first.Allowed || first.UsedToday != 2 || first.DailyLimit != 3 {
		t.Fatalf("first decision = %+v, want allowed 2/3", first)
	}
	snapshot, err := store.Snapshot(context.Background(), userID, domain, day.Add(12*time.Hour))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Status != sendramp.StatusRamping || snapshot.UsedToday != 2 || snapshot.DailyLimit != 3 || snapshot.ActiveDays != 1 {
		t.Fatalf("snapshot = %+v, want ramping active-day 1 at 2/3", snapshot)
	}
	retry := reserve(t, store, userID, domain, messageID, 2, day, schedule)
	if !retry.Allowed || retry.UsedToday != 2 {
		t.Fatalf("same-message retry = %+v, want idempotent 2/3", retry)
	}

	secondMessage := createMessageForAgent(t, pool, "agent@"+domain, "units-second")
	limited := reserve(t, store, userID, domain, secondMessage, 2, day, schedule)
	if limited.Allowed || limited.UsedToday != 2 || limited.DailyLimit != 3 {
		t.Fatalf("second decision = %+v, want limited at 2/3", limited)
	}
}

func TestReserveProgressesOnlyOnActiveDaysAndPersistsCompletion(t *testing.T) {
	store, pool, userID, domain, firstMessage := seedRampMessage(t, "active-days")
	schedule := sendramp.NewSchedule(1, 2, 2)
	day1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got := reserve(t, store, userID, domain, firstMessage, 1, day1, schedule); got.DailyLimit != 1 {
		t.Fatalf("day 1 = %+v, want limit 1", got)
	}

	// Ten idle calendar days do not advance the curve. The next active day is
	// active-day index 1, not calendar-day index 10.
	secondMessage := createMessageForAgent(t, pool, "agent@"+domain, "active-second")
	day11 := day1.AddDate(0, 0, 10)
	if got := reserve(t, store, userID, domain, secondMessage, 2, day11, schedule); !got.Allowed || got.DailyLimit != 2 {
		t.Fatalf("second active day = %+v, want allowed at target 2", got)
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
	schedule := sendramp.NewSchedule(1, 2, 2)

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
	schedule := sendramp.NewSchedule(1, 2, 2)
	day1 := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)

	if got := reserve(t, store, user.ID, domainA, messageA, 1, day1, schedule); !got.Allowed {
		t.Fatalf("first sibling = %+v, want allowed", got)
	}
	if got := reserve(t, store, user.ID, domainB, messageB, 1, day1, schedule); got.Allowed {
		t.Fatalf("second sibling = %+v, want shared day-one pool exhausted", got)
	}

	messageB2 := createMessageForAgent(t, pool, "agent@"+domainB, "two-second")
	if got := reserve(t, store, user.ID, domainB, messageB2, 2, day1.AddDate(0, 0, 1), schedule); !got.Allowed || got.DailyLimit != 2 {
		t.Fatalf("shared second active day = %+v, want target limit 2", got)
	}
}

func TestReserveConcurrentRecipientUnitsNeverExceedLimit(t *testing.T) {
	store, pool, userID, domain, firstMessage := seedRampMessage(t, "concurrent")
	messages := []string{firstMessage}
	for i := 1; i < 10; i++ {
		messages = append(messages, createMessageForAgent(t, pool, "agent@"+domain, fmt.Sprintf("concurrent-%d", i)))
	}
	day := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	schedule := sendramp.NewSchedule(11, 20, 2)

	var wg sync.WaitGroup
	allowedUnits := make(chan int, len(messages))
	for _, messageID := range messages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := store.Reserve(context.Background(), sendramp.ReserveRequest{
				MessageID: messageID, UserID: userID, Domain: domain,
				Units: 2, Day: day, Schedule: schedule,
			})
			if err != nil {
				t.Errorf("Reserve(%s): %v", messageID, err)
				return
			}
			if d.Allowed {
				allowedUnits <- 2
			}
		}()
	}
	wg.Wait()
	close(allowedUnits)
	total := 0
	for units := range allowedUnits {
		total += units
	}
	if total != 10 {
		t.Fatalf("concurrent allowed units = %d, want 10 (largest multiple of 2 <= 11)", total)
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
