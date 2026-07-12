package limits

import (
	"context"
	"sync"
	"time"

	"github.com/Mnexa-AI/e2a/internal/usage"
)

// Counter is the subset of *usage.Store the enforcer needs. Declared
// here as an interface so tests can supply a fake without standing up
// the full usage store.
type Counter interface {
	CountAgentsByUser(ctx context.Context, userID string) (int, error)
	CountDomainsByUser(ctx context.Context, userID string) (int, error)
	MessagesThisMonth(ctx context.Context, userID string) (int, error)
	GetStorageBytes(ctx context.Context, userID string) (int64, error)
}

// limitsReader is the subset of *Store the enforcer needs. Declared as
// an interface so tests can supply a fake without a real Postgres pool.
type limitsReader interface {
	Get(ctx context.Context, userID string) (Limits, bool, error)
}

// DBEnforcer is the production Enforcer: reads account_limits + falls
// back to operator Defaults, counts current resources via the usage
// store, and caches the resolved Limits in-process for cacheTTL to keep
// hot paths off the DB on every send.
//
// The cache only stores the *Limits* (caps), not the *counts*. Counts
// must always reflect the live database — caching them would mean a
// just-created agent or just-sent message wouldn't show up until TTL
// expiry, which would either let users exceed caps or let the dashboard
// lie about current usage. Reading the count is one bounded query per
// check; the win from caching limits is avoiding the join into
// account_limits, which is the costlier read.
type DBEnforcer struct {
	store    limitsReader
	counter  Counter
	defaults Defaults
	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cachedLimits
}

type cachedLimits struct {
	limits  Limits
	expires time.Time
}

// NewEnforcer constructs the production enforcer. cacheTTL of 0 disables
// the cache (every Get hits the DB) — useful for tests that mutate
// account_limits and want immediate visibility.
func NewEnforcer(store *Store, counter Counter, defaults Defaults, cacheTTL time.Duration) *DBEnforcer {
	return &DBEnforcer{
		store:    store,
		counter:  counter,
		defaults: defaults,
		cacheTTL: cacheTTL,
		cache:    make(map[string]cachedLimits),
	}
}

// newEnforcerWithReader is the test seam: lets unit tests inject a fake
// limitsReader so they don't need a Postgres pool.
func newEnforcerWithReader(reader limitsReader, counter Counter, defaults Defaults, cacheTTL time.Duration) *DBEnforcer {
	return &DBEnforcer{
		store:    reader,
		counter:  counter,
		defaults: defaults,
		cacheTTL: cacheTTL,
		cache:    make(map[string]cachedLimits),
	}
}

func (e *DBEnforcer) Get(ctx context.Context, userID string) (Limits, error) {
	if cached, ok := e.cacheGet(userID); ok {
		return cached, nil
	}
	row, found, err := e.store.Get(ctx, userID)
	if err != nil {
		return Limits{}, err
	}
	var resolved Limits
	if found {
		resolved = row
	} else {
		resolved = Limits{
			PlanCode:         e.defaults.PlanCode,
			MaxAgents:        e.defaults.MaxAgents,
			MaxDomains:       e.defaults.MaxDomains,
			MaxMessagesMonth: e.defaults.MaxMessagesMonth,
			MaxStorageBytes:  e.defaults.MaxStorageBytes,
		}
	}
	e.cachePut(userID, resolved)
	return resolved, nil
}

func (e *DBEnforcer) Invalidate(userID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cache, userID)
}

func (e *DBEnforcer) CheckAgentCreate(ctx context.Context, userID string) error {
	lim, err := e.Get(ctx, userID)
	if err != nil {
		return err
	}
	count, err := e.counter.CountAgentsByUser(ctx, userID)
	if err != nil {
		return err
	}
	if count >= lim.MaxAgents {
		return &LimitExceededError{
			Resource: "agents",
			Limit:    lim.MaxAgents,
			Current:  count,
			Limits:   lim,
		}
	}
	return nil
}

func (e *DBEnforcer) CheckDomainCreate(ctx context.Context, userID string) error {
	lim, err := e.Get(ctx, userID)
	if err != nil {
		return err
	}
	count, err := e.counter.CountDomainsByUser(ctx, userID)
	if err != nil {
		return err
	}
	if count >= lim.MaxDomains {
		return &LimitExceededError{
			Resource: "domains",
			Limit:    lim.MaxDomains,
			Current:  count,
			Limits:   lim,
		}
	}
	return nil
}

// CheckMessageSend enforces both the month-flow cap and the storage
// stock cap. Either being exceeded blocks the operation. The flow cap
// is checked first because it's the cheaper read; if both would fail,
// the user sees "messages_month" as the reason, which is the easier one
// to explain ("you sent N this month").
func (e *DBEnforcer) CheckMessageSend(ctx context.Context, userID string) error {
	lim, err := e.Get(ctx, userID)
	if err != nil {
		return err
	}
	msgCount, err := e.counter.MessagesThisMonth(ctx, userID)
	if err != nil {
		return err
	}
	if msgCount >= lim.MaxMessagesMonth {
		return &LimitExceededError{
			Resource: "messages_month",
			Limit:    lim.MaxMessagesMonth,
			Current:  msgCount,
			Limits:   lim,
		}
	}
	storage, err := e.counter.GetStorageBytes(ctx, userID)
	if err != nil {
		return err
	}
	if storage >= lim.MaxStorageBytes {
		// Storage values can exceed int32 (MaxStorageBytes is BIGINT in
		// the DB and may be 100 GiB+ on Scale). The LimitExceededError
		// fields are int — that's 32-bit on 32-bit platforms. Clamp via
		// safeInt64ToInt so we don't roll over on unusual targets; the
		// real value is also surfaced verbatim in the Limits field for
		// callers that need bytes-accurate access.
		return &LimitExceededError{
			Resource: "storage_bytes",
			Limit:    safeInt64ToInt(lim.MaxStorageBytes),
			Current:  safeInt64ToInt(storage),
			Limits:   lim,
		}
	}
	return nil
}

// safeInt64ToInt clamps an int64 to int.Max{,Min} so a downstream JSON
// encode never silently produces a negative number from int overflow
// on 32-bit Go targets. Production runs 64-bit; this is purely
// defensive against a future 32-bit build (or an ARM32 host).
func safeInt64ToInt(v int64) int {
	const maxInt = int64(^uint(0) >> 1)
	if v > maxInt {
		return int(maxInt)
	}
	return int(v)
}

func (e *DBEnforcer) cacheGet(userID string) (Limits, bool) {
	if e.cacheTTL <= 0 {
		return Limits{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cache[userID]
	if !ok || time.Now().After(c.expires) {
		return Limits{}, false
	}
	return c.limits, true
}

func (e *DBEnforcer) cachePut(userID string, l Limits) {
	if e.cacheTTL <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache[userID] = cachedLimits{limits: l, expires: time.Now().Add(e.cacheTTL)}
}

// Compile-time check: DBEnforcer satisfies the Enforcer interface and
// the Counter dependency is satisfied by *usage.Store.
var (
	_ Enforcer = (*DBEnforcer)(nil)
	_ Counter  = (*usage.Store)(nil)
)
