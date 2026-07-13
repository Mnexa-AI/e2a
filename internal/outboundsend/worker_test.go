package outboundsend_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/outboundsend"
)

type fakeStore struct {
	job         *outboundsend.SendJob
	loadErr     error
	markSentErr error
	releaseErr  error

	sent     []sentCall
	failed   []failedCall
	released []string
}

type sentCall struct{ id, provider, sentAs string }
type failedCall struct {
	id      string
	attempt int
	detail  string
}

func (f *fakeStore) ClaimSend(_ context.Context, _ string, _ int64) (*outboundsend.SendJob, error) {
	return f.job, f.loadErr
}
func (f *fakeStore) MarkSent(_ context.Context, id, provider, sentAs string) error {
	f.sent = append(f.sent, sentCall{id, provider, sentAs})
	return f.markSentErr
}
func (f *fakeStore) MarkFailed(_ context.Context, id string, attempt int, detail string) error {
	f.failed = append(f.failed, failedCall{id, attempt, detail})
	return nil
}
func (f *fakeStore) ReleaseSend(_ context.Context, id string, _ int64) error {
	f.released = append(f.released, id)
	return f.releaseErr
}

type fakeDeliverer struct{ out outboundsend.DeliverOutcome }

func (f fakeDeliverer) Deliver(_ context.Context, _ *outboundsend.SendJob) outboundsend.DeliverOutcome {
	return f.out
}

func job(id string, attempt int) *river.Job[outboundsend.OutboundSendArgs] {
	return &river.Job[outboundsend.OutboundSendArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: attempt, MaxAttempts: outboundsend.MaxSendAttempts, Kind: outboundsend.OutboundSendArgs{}.Kind()},
		Args:   outboundsend.OutboundSendArgs{MessageID: id},
	}
}

func acceptedJob(id string) *outboundsend.SendJob {
	return &outboundsend.SendJob{MessageID: id, Status: "accepted", EnvelopeFrom: "a@x.com", Recipients: []string{"b@y.com"}, RawMessage: []byte("raw")}
}

func TestSendWorker_Success(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1")}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "ses-1", SentAs: "relay"}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(st.sent) != 1 || st.sent[0].provider != "ses-1" || st.sent[0].sentAs != "relay" {
		t.Errorf("MarkSent = %+v, want one call with ses-1/relay", st.sent)
	}
	if len(st.failed) != 0 {
		t.Errorf("unexpected MarkFailed: %+v", st.failed)
	}
}

func TestSendWorker_AlreadyTerminalIsNoOp(t *testing.T) {
	st := &fakeStore{job: &outboundsend.SendJob{MessageID: "msg_1", Status: "sent"}}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "should-not-send"}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(st.sent) != 0 {
		t.Errorf("terminal message must not re-send, got MarkSent %+v", st.sent)
	}
}

func TestSendWorker_MessageGoneIsNoOp(t *testing.T) {
	st := &fakeStore{job: nil} // LoadForSend returns (nil, nil) → gone
	w := outboundsend.NewSendWorker(st, fakeDeliverer{})
	if err := w.Work(context.Background(), job("msg_gone", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(st.sent) != 0 || len(st.failed) != 0 {
		t.Errorf("gone message must be a no-op")
	}
}

func TestSendWorker_PermanentFailureCancels(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1")}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("recipient rejected 550"), Permanent: true}}
	w := outboundsend.NewSendWorker(st, dl)
	err := w.Work(context.Background(), job("msg_1", 1))
	if err == nil {
		t.Fatal("permanent failure should return a (cancel) error")
	}
	if len(st.failed) != 1 {
		t.Errorf("permanent failure must MarkFailed once, got %+v", st.failed)
	}
}

func TestSendWorker_LastAttemptFails(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1")}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("boom 421")}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", outboundsend.MaxSendAttempts)); err == nil {
		t.Fatal("final attempt failure should return an error so River discards")
	}
	if len(st.failed) != 1 {
		t.Errorf("final attempt must MarkFailed, got %+v", st.failed)
	}
}

func TestSendWorker_RetryableFailureDoesNotMarkFailed(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1")}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("transient 421")}}
	w := outboundsend.NewSendWorker(st, dl)
	err := w.Work(context.Background(), job("msg_1", 1))
	if err == nil {
		t.Fatal("retryable failure must return an error so River retries")
	}
	if len(st.failed) != 0 {
		t.Errorf("a non-final retryable failure must NOT MarkFailed (status stays accepted), got %+v", st.failed)
	}
	if len(st.sent) != 0 {
		t.Errorf("failed send must not MarkSent")
	}
	if len(st.released) != 1 || st.released[0] != "msg_1" {
		t.Errorf("retryable failure must release the active claim, got %v", st.released)
	}
}

func TestSendWorker_RetryableFailureReleaseErrorRetries(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1"), releaseErr: errors.New("db unavailable")}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("transient 421")}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err == nil || !errors.Is(err, st.releaseErr) {
		t.Fatalf("Work error = %v, want release error", err)
	}
	if len(st.released) != 1 {
		t.Fatalf("release calls = %v, want one", st.released)
	}
}

func TestSendWorker_OutageSnoozesWithoutBurningAttempt(t *testing.T) {
	j := acceptedJob("msg_1")
	j.AcceptedAt = time.Now() // fresh accept — within the retry horizon
	st := &fakeStore{job: j}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("dial tcp 1.2.3.4:587: i/o timeout"), Outage: true}}
	w := outboundsend.NewSendWorker(st, dl)
	// Even at a high attempt number, an outage must snooze (not terminal-fail):
	// JobSnooze doesn't count the attempt, so MaxSendAttempts is never reached.
	err := w.Work(context.Background(), job("msg_1", outboundsend.MaxSendAttempts))
	if err == nil {
		t.Fatal("provider outage should snooze (non-nil JobSnooze error)")
	}
	if len(st.failed) != 0 {
		t.Errorf("an outage within the horizon must NOT MarkFailed, got %+v", st.failed)
	}
	if len(st.sent) != 0 {
		t.Errorf("an outage must not MarkSent")
	}
	if len(st.released) != 1 || st.released[0] != "msg_1" {
		t.Errorf("outage snooze must release the active claim, got %v", st.released)
	}
}

func TestSendWorker_OutagePastHorizonFailsTerminally(t *testing.T) {
	j := acceptedJob("msg_1")
	j.AcceptedAt = time.Now().Add(-73 * time.Hour) // past the 72h horizon
	st := &fakeStore{job: j}
	dl := fakeDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("connection refused"), Outage: true}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 2)); err == nil {
		t.Fatal("an outage past the retry horizon should fail terminally")
	}
	if len(st.failed) != 1 {
		t.Errorf("an outage past the horizon must MarkFailed, got %+v", st.failed)
	}
}

func TestSendWorker_NextRetryMatchesEnvelope(t *testing.T) {
	w := outboundsend.NewSendWorker(nil, nil)
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 1 * time.Hour, 4 * time.Hour}
	for i, d := range want {
		got := time.Until(w.NextRetry(job("x", i))).Round(time.Second)
		if diff := got - d; diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("attempt %d: next retry in %v, want ~%v", i, got, d)
		}
	}
}
