package outboundsend_test

// Final suppression guard before provider I/O. A suppression added AFTER
// approval/acceptance (while the job sat on the queue) must still prevent
// delivery: the worker re-checks the owning account's suppression list before
// the SMTP submit, records the terminal failure (delivery_status='failed' +
// email.failed via MarkFailed) WITHOUT calling the provider, and cancels the
// job. A suppression-store error fails CLOSED — the claim is released and the
// job returns an error for River to retry, because silently sending would
// break the published "addresses e2a will refuse to send to" contract
// (GET /v1/account/suppressions).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/outboundsend"
)

// trippingDeliverer fails the test if any provider I/O is attempted.
type trippingDeliverer struct{ t *testing.T }

func (d trippingDeliverer) Deliver(_ context.Context, j *outboundsend.SendJob) outboundsend.DeliverOutcome {
	d.t.Errorf("provider Deliver called for %s despite suppression guard", j.MessageID)
	return outboundsend.DeliverOutcome{}
}

func TestSendWorker_SuppressedRecipientFailsTerminallyWithoutProviderIO(t *testing.T) {
	j := acceptedJob("msg_1")
	j.Domain, j.MessageType, j.SentAs = "new.example.com", "send", "own_address"
	st := &fakeStore{job: j, suppressed: []string{"b@y.com"}}
	gate := &fakeRampGate{}
	w := outboundsend.NewSendWorker(st, trippingDeliverer{t}, gate)

	err := w.Work(context.Background(), job("msg_1", 1))
	if err == nil {
		t.Fatal("suppressed send must cancel the job (non-nil error)")
	}
	if len(st.failed) != 1 {
		t.Fatalf("MarkFailed calls = %+v, want exactly one terminal failure", st.failed)
	}
	if !strings.Contains(st.failed[0].detail, "recipient_suppressed") || !strings.Contains(st.failed[0].detail, "b@y.com") {
		t.Errorf("failure detail = %q, want recipient_suppressed naming the address", st.failed[0].detail)
	}
	if len(st.sent) != 0 {
		t.Errorf("MarkSent = %+v, want none", st.sent)
	}
	if st.suppressionUserID != st.job.UserID {
		t.Errorf("suppression check scoped to %q, want the job's owning account %q", st.suppressionUserID, st.job.UserID)
	}
	if len(gate.released) != 1 || gate.released[0] != "msg_1" {
		t.Errorf("ramp releases = %v, want [msg_1]", gate.released)
	}
}

// A store error on the guard is conservative: no provider I/O, no terminal
// failure — release the side-effect-free claim and let River retry.
func TestSendWorker_SuppressionCheckErrorFailsClosed(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1"), suppressedErr: errors.New("suppression store down")}
	w := outboundsend.NewSendWorker(st, trippingDeliverer{t})

	err := w.Work(context.Background(), job("msg_1", 1))
	if err == nil {
		t.Fatal("suppression-store error must return an error (River retries), not send")
	}
	if len(st.failed) != 0 {
		t.Errorf("MarkFailed = %+v, want none (a store blip is not a terminal outcome)", st.failed)
	}
	if len(st.sent) != 0 {
		t.Errorf("MarkSent = %+v, want none", st.sent)
	}
	if len(st.released) != 1 || st.released[0] != "msg_1" {
		t.Errorf("released claims = %v, want [msg_1]", st.released)
	}
}

func TestSendWorker_UnsuppressedRecipientStillSends(t *testing.T) {
	st := &fakeStore{job: acceptedJob("msg_1")} // no suppressions
	dl := &fakeDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "ses-ok", SentAs: "relay"}}
	w := outboundsend.NewSendWorker(st, dl)
	if err := w.Work(context.Background(), job("msg_1", 1)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(st.sent) != 1 || st.sent[0].provider != "ses-ok" {
		t.Errorf("MarkSent = %+v, want one successful send", st.sent)
	}
	if len(st.failed) != 0 {
		t.Errorf("MarkFailed = %+v, want none", st.failed)
	}
}
