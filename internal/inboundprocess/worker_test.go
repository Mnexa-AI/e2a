package inboundprocess_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/inboundprocess"
)

type fakeStore struct {
	intake  *identity.InboundIntake
	loadErr error
	failed  []string // intake ids marked failed
}

func (f *fakeStore) LoadInboundIntake(_ context.Context, _ string) (*identity.InboundIntake, error) {
	return f.intake, f.loadErr
}

func (f *fakeStore) MarkInboundIntakeFailed(_ context.Context, id, _ string) error {
	f.failed = append(f.failed, id)
	return nil
}

func (f *fakeStore) StampInboundIntakeJobIDTx(_ context.Context, _ pgx.Tx, _ string, _ int64) error {
	return nil
}

func (f *fakeStore) PruneProcessedIntake(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type fakeProcessor struct {
	err   error
	calls int
}

func (f *fakeProcessor) ProcessIntake(_ context.Context, _ *identity.InboundIntake) error {
	f.calls++
	return f.err
}

func job(attempt int) *river.Job[inboundprocess.InboundProcessArgs] {
	return &river.Job[inboundprocess.InboundProcessArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: inboundprocess.MaxInboundAttempts, Kind: inboundprocess.InboundProcessArgs{}.Kind()},
		Args:   inboundprocess.InboundProcessArgs{IntakeID: "intk_1"},
	}
}

func accepted() *identity.InboundIntake {
	return &identity.InboundIntake{ID: "intk_1", Status: identity.IntakeStatusAccepted, Recipient: "bot@x.test", Raw: []byte("raw")}
}

func TestWorker_Success(t *testing.T) {
	st := &fakeStore{intake: accepted()}
	pr := &fakeProcessor{}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(0)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if pr.calls != 1 {
		t.Errorf("ProcessIntake calls = %d, want 1", pr.calls)
	}
	if len(st.failed) != 0 {
		t.Errorf("no MarkFailed on success, got %v", st.failed)
	}
}

func TestWorker_GoneIsNoOp(t *testing.T) {
	st := &fakeStore{intake: nil} // pruned between accept and processing
	pr := &fakeProcessor{}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(0)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if pr.calls != 0 {
		t.Error("a gone intake must not be processed")
	}
}

func TestWorker_AlreadyProcessedIsNoOp(t *testing.T) {
	it := accepted()
	it.Status = identity.IntakeStatusProcessed
	st := &fakeStore{intake: it}
	pr := &fakeProcessor{}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(0)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if pr.calls != 0 {
		t.Error("an already-processed intake must not be re-processed (idempotency gate)")
	}
}

// TestWorker_RecipientGoneMarksTerminal: a gone recipient (deleted agent) is dropped
// terminally (intake marked failed), NOT retried — no error, so the job completes and
// the intake doesn't linger 'accepted' with orphaned raw MIME.
func TestWorker_RecipientGoneMarksTerminal(t *testing.T) {
	st := &fakeStore{intake: accepted()}
	pr := &fakeProcessor{err: identity.ErrRecipientGone}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(0)); err != nil {
		t.Fatalf("a gone recipient should drop (no error), got %v", err)
	}
	if len(st.failed) != 1 || st.failed[0] != "intk_1" {
		t.Errorf("a gone recipient must mark the intake terminal, got %v", st.failed)
	}
}

func TestWorker_TransientRetryDoesNotMarkFailed(t *testing.T) {
	st := &fakeStore{intake: accepted()}
	pr := &fakeProcessor{err: errors.New("db blip")}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(0)); err == nil {
		t.Fatal("a transient processing error should return a retryable error")
	}
	if len(st.failed) != 0 {
		t.Errorf("an early attempt must NOT MarkFailed, got %v", st.failed)
	}
}

// TestJobs_ProcessIntakeLateBinding covers the late-bound Processor: before
// SetProcessor, Jobs.ProcessIntake returns a retryable error (no panic); after, it
// delegates to the concrete processor.
func TestJobs_ProcessIntakeLateBinding(t *testing.T) {
	j := inboundprocess.NewJobs(&fakeStore{})
	if err := j.ProcessIntake(context.Background(), accepted()); err == nil {
		t.Fatal("an unwired processor should return a retryable error, not panic")
	}
	pr := &fakeProcessor{}
	j.SetProcessor(pr)
	if err := j.ProcessIntake(context.Background(), accepted()); err != nil {
		t.Fatalf("wired ProcessIntake: %v", err)
	}
	if pr.calls != 1 {
		t.Errorf("wired ProcessIntake should delegate once, got %d", pr.calls)
	}
}

func TestWorker_FinalAttemptMarksFailed(t *testing.T) {
	st := &fakeStore{intake: accepted()}
	pr := &fakeProcessor{err: errors.New("db blip")}
	w := inboundprocess.NewInboundProcessWorker(st, pr)
	if err := w.Work(context.Background(), job(inboundprocess.MaxInboundAttempts)); err == nil {
		t.Fatal("the final attempt should still return an error so River discards the job")
	}
	if len(st.failed) != 1 || st.failed[0] != "intk_1" {
		t.Errorf("the final attempt must MarkFailed the intake, got %v", st.failed)
	}
}
