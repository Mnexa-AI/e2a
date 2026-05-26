package limits

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore implements limitsReader. Test stubs use this to control
// what Get returns without standing up Postgres.
type fakeStore struct {
	mu    sync.Mutex
	row   Limits
	found bool
	err   error
	calls int
}

func (f *fakeStore) Get(ctx context.Context, userID string) (Limits, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.row, f.found, f.err
}

// fakeCounter implements Counter. Counts and storage are set directly
// by the test; calls increment so tests can assert no-cache behaviour
// on the count side.
type fakeCounter struct {
	mu              sync.Mutex
	agents          int
	domains         int
	messagesMonth   int
	storageBytes    int64
	countAgentsErr  error
	countDomainsErr error
	messagesErr     error
	storageErr      error
}

func (f *fakeCounter) CountAgentsByUser(ctx context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents, f.countAgentsErr
}
func (f *fakeCounter) CountDomainsByUser(ctx context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.domains, f.countDomainsErr
}
func (f *fakeCounter) MessagesThisMonth(ctx context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messagesMonth, f.messagesErr
}
func (f *fakeCounter) GetStorageBytes(ctx context.Context, userID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.storageBytes, f.storageErr
}

func defaultsForTest() Defaults {
	return Defaults{
		PlanCode:         "default",
		MaxAgents:        5,
		MaxDomains:       2,
		MaxMessagesMonth: 100,
		MaxStorageBytes:  10_000,
	}
}

func TestEnforcer_GetFallsBackToDefaultsWhenNoRow(t *testing.T) {
	store := &fakeStore{found: false}
	counter := &fakeCounter{}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	got, err := e.Get(context.Background(), "user1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MaxAgents != 5 || got.MaxDomains != 2 || got.MaxMessagesMonth != 100 || got.MaxStorageBytes != 10_000 {
		t.Errorf("Get fallback = %+v, want defaults", got)
	}
	if got.PlanCode != "default" {
		t.Errorf("PlanCode = %q, want default", got.PlanCode)
	}
	if got.UpgradeURL != "" {
		t.Errorf("UpgradeURL = %q on fallback, want empty", got.UpgradeURL)
	}
}

func TestEnforcer_GetReturnsRowWhenPresent(t *testing.T) {
	row := Limits{
		PlanCode: "pro", MaxAgents: 50, MaxDomains: 10,
		MaxMessagesMonth: 50_000, MaxStorageBytes: 10 << 30,
		UpgradeURL: "https://billing.example/portal",
	}
	store := &fakeStore{row: row, found: true}
	counter := &fakeCounter{}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	got, err := e.Get(context.Background(), "user1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != row {
		t.Errorf("Get = %+v, want %+v", got, row)
	}
}

func TestEnforcer_GetPropagatesStoreError(t *testing.T) {
	sentinel := errors.New("db down")
	store := &fakeStore{err: sentinel}
	e := newEnforcerWithReader(store, &fakeCounter{}, defaultsForTest(), 0)

	_, err := e.Get(context.Background(), "user1")
	if !errors.Is(err, sentinel) {
		t.Errorf("Get error = %v, want %v", err, sentinel)
	}
}

func TestEnforcer_CacheServesFromMemoryWithinTTL(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{PlanCode: "p", MaxAgents: 1, MaxDomains: 1, MaxMessagesMonth: 1, MaxStorageBytes: 1}}
	e := newEnforcerWithReader(store, &fakeCounter{}, defaultsForTest(), time.Minute)

	for i := 0; i < 5; i++ {
		if _, err := e.Get(context.Background(), "user1"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if store.calls != 1 {
		t.Errorf("store hit %d times, want 1 (cache should serve subsequent)", store.calls)
	}
}

func TestEnforcer_InvalidateClearsCachedRow(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{MaxAgents: 1, MaxDomains: 1, MaxMessagesMonth: 1, MaxStorageBytes: 1}}
	e := newEnforcerWithReader(store, &fakeCounter{}, defaultsForTest(), time.Minute)

	_, _ = e.Get(context.Background(), "user1")
	e.Invalidate("user1")
	_, _ = e.Get(context.Background(), "user1")
	if store.calls != 2 {
		t.Errorf("store hit %d times after invalidate, want 2", store.calls)
	}
}

func TestCheckAgentCreate_AllowsBelowCap(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{MaxAgents: 3, MaxDomains: 1, MaxMessagesMonth: 1, MaxStorageBytes: 1}}
	counter := &fakeCounter{agents: 2}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	if err := e.CheckAgentCreate(context.Background(), "user1"); err != nil {
		t.Errorf("CheckAgentCreate at 2/3: %v, want nil", err)
	}
}

func TestCheckAgentCreate_BlocksAtCap(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{PlanCode: "free", MaxAgents: 3, MaxDomains: 1, MaxMessagesMonth: 1, MaxStorageBytes: 1, UpgradeURL: "https://up"}}
	counter := &fakeCounter{agents: 3}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	err := e.CheckAgentCreate(context.Background(), "user1")
	le, ok := IsLimitExceeded(err)
	if !ok {
		t.Fatalf("CheckAgentCreate at cap = %v, want LimitExceededError", err)
	}
	if le.Resource != "agents" {
		t.Errorf("Resource = %q, want agents", le.Resource)
	}
	if le.Limit != 3 || le.Current != 3 {
		t.Errorf("Limit/Current = %d/%d, want 3/3", le.Limit, le.Current)
	}
	if le.Limits.UpgradeURL != "https://up" {
		t.Errorf("UpgradeURL not propagated to error: %q", le.Limits.UpgradeURL)
	}
}

func TestCheckDomainCreate_BlocksAtCap(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{MaxAgents: 100, MaxDomains: 1, MaxMessagesMonth: 1, MaxStorageBytes: 1}}
	counter := &fakeCounter{domains: 1}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	err := e.CheckDomainCreate(context.Background(), "user1")
	le, ok := IsLimitExceeded(err)
	if !ok {
		t.Fatalf("CheckDomainCreate at cap = %v, want LimitExceededError", err)
	}
	if le.Resource != "domains" {
		t.Errorf("Resource = %q, want domains", le.Resource)
	}
}

func TestCheckMessageSend_BlocksOnMonthFlow(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{MaxAgents: 100, MaxDomains: 100, MaxMessagesMonth: 1000, MaxStorageBytes: 1 << 40}}
	counter := &fakeCounter{messagesMonth: 1000, storageBytes: 0}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	err := e.CheckMessageSend(context.Background(), "user1")
	le, ok := IsLimitExceeded(err)
	if !ok {
		t.Fatalf("CheckMessageSend at month cap = %v, want LimitExceededError", err)
	}
	if le.Resource != "messages" {
		t.Errorf("Resource = %q, want messages", le.Resource)
	}
}

func TestCheckMessageSend_BlocksOnStorage(t *testing.T) {
	store := &fakeStore{found: true, row: Limits{MaxAgents: 100, MaxDomains: 100, MaxMessagesMonth: 1_000_000, MaxStorageBytes: 1024 * 1024}}
	counter := &fakeCounter{messagesMonth: 10, storageBytes: 1024 * 1024}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	err := e.CheckMessageSend(context.Background(), "user1")
	le, ok := IsLimitExceeded(err)
	if !ok {
		t.Fatalf("CheckMessageSend at storage cap = %v, want LimitExceededError", err)
	}
	if le.Resource != "storage" {
		t.Errorf("Resource = %q, want storage", le.Resource)
	}
	// Storage values come through as RAW BYTES (not KB) per the
	// resource-natural-unit contract documented on LimitErrorBody.
	if le.Limit != 1024*1024 {
		t.Errorf("Limit = %d, want %d (bytes)", le.Limit, 1024*1024)
	}
	if le.Current != 1024*1024 {
		t.Errorf("Current = %d, want %d (bytes)", le.Current, 1024*1024)
	}
}

func TestCheckMessageSend_MonthCapTakesPrecedenceOverStorage(t *testing.T) {
	// Both caps hit; the enforcer reports the cheaper-to-explain one.
	store := &fakeStore{found: true, row: Limits{MaxAgents: 100, MaxDomains: 100, MaxMessagesMonth: 1000, MaxStorageBytes: 1024}}
	counter := &fakeCounter{messagesMonth: 1000, storageBytes: 1024}
	e := newEnforcerWithReader(store, counter, defaultsForTest(), 0)

	err := e.CheckMessageSend(context.Background(), "user1")
	le, ok := IsLimitExceeded(err)
	if !ok {
		t.Fatalf("want LimitExceededError")
	}
	if le.Resource != "messages" {
		t.Errorf("Resource = %q, want messages", le.Resource)
	}
}

func TestLimitExceededError_FormatsResourceAndCounts(t *testing.T) {
	e := &LimitExceededError{Resource: "agents", Limit: 3, Current: 3}
	if got := e.Error(); got != "limits: agents cap reached (3/3)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestIsLimitExceeded_WrappedError(t *testing.T) {
	base := &LimitExceededError{Resource: "agents", Limit: 1, Current: 1}
	wrapped := wrap(base)
	got, ok := IsLimitExceeded(wrapped)
	if !ok || got != base {
		t.Errorf("IsLimitExceeded did not unwrap: ok=%v got=%v", ok, got)
	}
}

// wrap exists to test errors.As traversal through fmt.Errorf chains —
// real callers will commonly wrap with context.
func wrap(err error) error {
	type wrapper struct{ inner error }
	return &wrappedErr{inner: err}
}

type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }
