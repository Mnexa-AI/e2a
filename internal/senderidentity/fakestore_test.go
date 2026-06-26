package senderidentity

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
)

// fakeStore is an in-memory Store for unit tests. Concurrency-safe so it can
// be driven from worker goroutines if needed. Domains not present in the
// status map are treated as "gone" → GetSendingStatus returns pgx.ErrNoRows.
type fakeStore struct {
	mu sync.Mutex

	// status holds the current sending status per domain. Absence ⇒ row gone.
	status map[string]Status
	// owners maps domain → owning user_id ("" or absent ⇒ no owner).
	owners map[string]string
	// live marks domains DomainExists should report true for.
	live map[string]bool

	// provisionInputs feeds SendingProvisionInputs.
	selector  string
	privKey   []byte
	inputsOK  bool
	inputsErr error

	// forced errors for the write/read methods (for retry-path tests).
	setStatusErr error
	touchErr     error
	getStatusErr error

	// recorded calls
	SetStatusCalls []setStatusCall
	TouchCalls     []string
}

type setStatusCall struct {
	Domain         string
	Status         Status
	DkimStatus     Status
	MailFromStatus Status
	ErrMsg         string
	Records        []DNSRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		status: map[string]Status{},
		owners: map[string]string{},
		live:   map[string]bool{},
	}
}

func (s *fakeStore) setStatus(domain string, st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[domain] = st
}

func (s *fakeStore) setOwner(domain, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owners[domain] = owner
}

func (s *fakeStore) setLive(domain string, live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[domain] = live
}

func (s *fakeStore) setProvisionInputs(selector string, key []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selector = selector
	s.privKey = key
	s.inputsOK = ok
}

func (s *fakeStore) lastSetStatus() (setStatusCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.SetStatusCalls) == 0 {
		return setStatusCall{}, false
	}
	return s.SetStatusCalls[len(s.SetStatusCalls)-1], true
}

func (s *fakeStore) SendingProvisionInputs(ctx context.Context, domain string) (string, []byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputsErr != nil {
		return "", nil, false, s.inputsErr
	}
	return s.selector, s.privKey, s.inputsOK, nil
}

func (s *fakeStore) SetSendingStatus(ctx context.Context, domain string, status, dkimStatus, mailFromStatus Status, errMsg string, records []DNSRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setStatusErr != nil {
		return s.setStatusErr
	}
	s.SetStatusCalls = append(s.SetStatusCalls, setStatusCall{Domain: domain, Status: status, DkimStatus: dkimStatus, MailFromStatus: mailFromStatus, ErrMsg: errMsg, Records: records})
	s.status[domain] = status
	return nil
}

func (s *fakeStore) TouchSendingChecked(ctx context.Context, domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.touchErr != nil {
		return s.touchErr
	}
	s.TouchCalls = append(s.TouchCalls, domain)
	return nil
}

func (s *fakeStore) GetSendingStatus(ctx context.Context, domain string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getStatusErr != nil {
		return "", s.getStatusErr
	}
	st, ok := s.status[domain]
	if !ok {
		return "", pgx.ErrNoRows // domain deleted mid-flight
	}
	return st, nil
}

func (s *fakeStore) DomainOwner(ctx context.Context, domain string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owners[domain], nil
}

func (s *fakeStore) DomainExists(ctx context.Context, domain string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[domain], nil
}

// recordingFirer captures EventFirer invocations.
type recordingFirer struct {
	mu    sync.Mutex
	calls []firedEvent
}

type firedEvent struct {
	Domain string
	UserID string
	Status Status
	ErrMsg string
}

func (r *recordingFirer) fire() EventFirer {
	return func(ctx context.Context, domain, userID string, status Status, errMsg string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, firedEvent{Domain: domain, UserID: userID, Status: status, ErrMsg: errMsg})
	}
}

func (r *recordingFirer) last() (firedEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return firedEvent{}, false
	}
	return r.calls[len(r.calls)-1], true
}

func (r *recordingFirer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
