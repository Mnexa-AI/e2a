package hitlnotify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/identity"
)

type fakeStore struct {
	pn      *identity.PendingNotify
	loadErr error
	markErr error

	notified []string
}

func (f *fakeStore) LoadPendingNotify(_ context.Context, _ string) (*identity.PendingNotify, error) {
	return f.pn, f.loadErr
}
func (f *fakeStore) MarkMessageNotified(_ context.Context, id string) error {
	f.notified = append(f.notified, id)
	return f.markErr
}
func (f *fakeStore) StampNotifyJobIDTx(_ context.Context, _ pgx.Tx, _ string, _ int64) error {
	return nil
}

type fakeDeliverer struct {
	out    hitlnotify.DeliverOutcome
	called int
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ *identity.PendingNotify) hitlnotify.DeliverOutcome {
	f.called++
	return f.out
}

func job(id string, attempt int) *river.Job[hitlnotify.HITLNotifyArgs] {
	return &river.Job[hitlnotify.HITLNotifyArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: hitlnotify.MaxNotifyAttempts, Kind: hitlnotify.HITLNotifyArgs{}.Kind()},
		Args:   hitlnotify.HITLNotifyArgs{MessageID: id},
	}
}

// pending builds a live pending_review PendingNotify (TTL in the future).
func pending(id string) *identity.PendingNotify {
	exp := time.Now().Add(1 * time.Hour)
	return &identity.PendingNotify{
		Message: &identity.Message{ID: id, Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp},
		Agent:   &identity.AgentIdentity{ID: "agent@x.test"},
	}
}

func TestNotifyWorker_Success(t *testing.T) {
	st := &fakeStore{pn: pending("msg_1")}
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if dl.called != 1 {
		t.Errorf("Deliver called %d times, want 1", dl.called)
	}
	if len(st.notified) != 1 || st.notified[0] != "msg_1" {
		t.Errorf("MarkMessageNotified = %v, want [msg_1]", st.notified)
	}
}

func TestNotifyWorker_MessageGoneIsNoOp(t *testing.T) {
	st := &fakeStore{pn: nil} // LoadPendingNotify returns (nil, nil) → gone
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_gone", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if dl.called != 0 || len(st.notified) != 0 {
		t.Errorf("gone message must be a no-op (deliver=%d notified=%v)", dl.called, st.notified)
	}
}

func TestNotifyWorker_ResolvedIsNoOp(t *testing.T) {
	pn := pending("msg_1")
	pn.Message.Status = identity.MessageStatusSent // approved/resolved before we notified
	st := &fakeStore{pn: pn}
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if dl.called != 0 {
		t.Errorf("a resolved hold must not send, Deliver called %d", dl.called)
	}
}

func TestNotifyWorker_ExpiredHoldIsNoOp(t *testing.T) {
	pn := pending("msg_1")
	past := time.Now().Add(-1 * time.Minute)
	pn.Message.ApprovalExpiresAt = &past
	st := &fakeStore{pn: pn}
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if dl.called != 0 {
		t.Errorf("an expired hold must not send, Deliver called %d", dl.called)
	}
}

func TestNotifyWorker_AlreadyNotifiedIsNoOp(t *testing.T) {
	pn := pending("msg_1")
	pn.Notified = true // notified_at already set (crash-after-send re-drive)
	st := &fakeStore{pn: pn}
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if dl.called != 0 {
		t.Errorf("an already-notified hold must not re-send, Deliver called %d", dl.called)
	}
}

func TestNotifyWorker_MarkFailedAfterSendDoesNotRetry(t *testing.T) {
	// The email is already out; only the dedup marker failed. Returning an error
	// would re-send on retry, so Work must swallow it and complete.
	st := &fakeStore{pn: pending("msg_1"), markErr: errors.New("db blip")}
	dl := &fakeDeliverer{}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("mark-notified failure after a successful send must not error, got %v", err)
	}
}

func TestNotifyWorker_PermanentCancels(t *testing.T) {
	st := &fakeStore{pn: pending("msg_1")}
	dl := &fakeDeliverer{out: hitlnotify.DeliverOutcome{Err: errors.New("550 user unknown"), Permanent: true}}
	w := hitlnotify.NewNotifyWorker(st, dl)
	err := w.Work(context.Background(), job("msg_1", 1))
	if err == nil {
		t.Fatal("a permanent failure should return a (cancel) error")
	}
	if len(st.notified) != 0 {
		t.Errorf("a failed send must not mark notified, got %v", st.notified)
	}
}

func TestNotifyWorker_OutageSnoozes(t *testing.T) {
	st := &fakeStore{pn: pending("msg_1")}
	dl := &fakeDeliverer{out: hitlnotify.DeliverOutcome{Err: errors.New("connection refused"), Outage: true}}
	w := hitlnotify.NewNotifyWorker(st, dl)
	// Even at a high attempt number an outage must snooze (JobSnooze doesn't burn
	// an attempt), never terminal-fail.
	err := w.Work(context.Background(), job("msg_1", hitlnotify.MaxNotifyAttempts))
	if err == nil {
		t.Fatal("a relay outage should snooze (non-nil JobSnooze error)")
	}
	if len(st.notified) != 0 {
		t.Errorf("an outage must not mark notified, got %v", st.notified)
	}
}

func TestNotifyWorker_TransientRetries(t *testing.T) {
	st := &fakeStore{pn: pending("msg_1")}
	dl := &fakeDeliverer{out: hitlnotify.DeliverOutcome{Err: errors.New("owner lookup blip")}}
	w := hitlnotify.NewNotifyWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err == nil {
		t.Fatal("a transient failure must return an error so River retries")
	}
	if len(st.notified) != 0 {
		t.Errorf("a failed send must not mark notified, got %v", st.notified)
	}
}

func TestNotifyWorker_LoadErrorRetries(t *testing.T) {
	st := &fakeStore{loadErr: errors.New("db down")}
	w := hitlnotify.NewNotifyWorker(st, &fakeDeliverer{})
	if err := w.Work(context.Background(), job("msg_1", 1)); err == nil {
		t.Fatal("a load error must propagate so River retries")
	}
}

func TestNotifyWorker_NextRetryMatchesEnvelope(t *testing.T) {
	w := hitlnotify.NewNotifyWorker(nil, nil)
	want := []time.Duration{15 * time.Second, 1 * time.Minute, 5 * time.Minute, 15 * time.Minute, 1 * time.Hour}
	for i, d := range want {
		got := time.Until(w.NextRetry(job("x", i))).Round(time.Second)
		if diff := got - d; diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("attempt %d: next retry in %v, want ~%v", i, got, d)
		}
	}
}
