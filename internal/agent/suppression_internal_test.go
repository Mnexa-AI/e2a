package agent

// Unit coverage of the suppression-check core, including the DELIBERATE split
// in store-error handling:
//
//   - Accept-time (direct send, checkSuppression): fails OPEN on a store error
//     — a suppression-DB hiccup must not block legitimate mail at intake, and
//     the async pipeline's pre-provider guard (internal/outboundsend) now
//     backstops the promise before any SES I/O.
//   - Approval-time (checkSuppressionStrict): fails CLOSED — approving is a
//     human-driven, freely retryable action, and GET /v1/account/suppressions
//     publicly promises "addresses e2a will refuse to send to", so a store
//     error refuses (retryable internal_error) rather than silently sending.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/outbound"
)

type stubSuppressionLister struct {
	suppressed []string
	err        error
	gotUserID  string
	gotAgentID string
	gotAddrs   []string
}

func (s *stubSuppressionLister) EffectiveSuppressions(_ context.Context, userID, agentID string, addrs []string) ([]string, error) {
	s.gotUserID = userID
	s.gotAgentID = agentID
	s.gotAddrs = addrs
	return s.suppressed, s.err
}

func TestCheckSuppressionCore_ChecksFullRecipientSet(t *testing.T) {
	stub := &stubSuppressionLister{}
	req := outbound.SendRequest{
		To:  []string{"a@x.test"},
		CC:  []string{"b@x.test"},
		BCC: []string{"c@x.test"},
	}
	if oerr := checkSuppressionCore(context.Background(), stub, "user_1", "sender@agents.test", req, true); oerr != nil {
		t.Fatalf("unexpected error: %+v", oerr)
	}
	if stub.gotUserID != "user_1" {
		t.Errorf("userID = %q, want user_1 (owner-scoped)", stub.gotUserID)
	}
	if stub.gotAgentID != "sender@agents.test" {
		t.Errorf("agentID = %q, want sender@agents.test", stub.gotAgentID)
	}
	if len(stub.gotAddrs) != 3 {
		t.Errorf("checked addrs = %v, want the full To+CC+BCC set", stub.gotAddrs)
	}
}

func TestCheckSuppressionCore_SuppressedIs422(t *testing.T) {
	stub := &stubSuppressionLister{suppressed: []string{"a@x.test"}}
	req := outbound.SendRequest{To: []string{"a@x.test"}}
	for _, failClosed := range []bool{false, true} {
		oerr := checkSuppressionCore(context.Background(), stub, "user_1", "sender@agents.test", req, failClosed)
		if oerr == nil || oerr.Status != http.StatusUnprocessableEntity || oerr.Code != "recipient_suppressed" {
			t.Fatalf("failClosed=%v: error = %+v, want 422 recipient_suppressed", failClosed, oerr)
		}
		if !strings.Contains(oerr.Msg, "/v1/account/suppressions/{address}") ||
			!strings.Contains(oerr.Msg, "/v1/agents/sender@agents.test/suppressions/{address}?confirm=DELETE") {
			t.Fatalf("remediation = %q, want both account-wide and exact-agent endpoints", oerr.Msg)
		}
		if strings.Contains(oerr.Msg, "remove via DELETE /v1/account") {
			t.Fatalf("remediation falsely implies account deletion alone is sufficient: %q", oerr.Msg)
		}
	}
}

// A store error fails OPEN at accept time (failClosed=false — the documented
// legacy tradeoff) and CLOSED at approval time (failClosed=true — refuse with
// a retryable error rather than silently sending; the hold stays pending).
func TestCheckSuppressionCore_StoreErrorHonorsFailMode(t *testing.T) {
	stub := &stubSuppressionLister{err: errors.New("db down")}
	req := outbound.SendRequest{To: []string{"a@x.test"}}

	if oerr := checkSuppressionCore(context.Background(), stub, "user_1", "sender@agents.test", req, false); oerr != nil {
		t.Fatalf("accept-time check must fail open on a store error, got %+v", oerr)
	}
	oerr := checkSuppressionCore(context.Background(), stub, "user_1", "sender@agents.test", req, true)
	if oerr == nil {
		t.Fatal("approval-time check must fail closed on a store error")
	}
	if oerr.Status != http.StatusInternalServerError || oerr.Code != "internal_error" {
		t.Fatalf("fail-closed error = %d %s, want a retryable 500 internal_error", oerr.Status, oerr.Code)
	}
}

func TestCheckSuppressionCore_NoRecipientsNoLookup(t *testing.T) {
	stub := &stubSuppressionLister{err: errors.New("must not be called")}
	if oerr := checkSuppressionCore(context.Background(), stub, "user_1", "sender@agents.test", outbound.SendRequest{}, true); oerr != nil {
		t.Fatalf("empty recipient set must be a no-op, got %+v", oerr)
	}
	if stub.gotAddrs != nil {
		t.Errorf("lookup ran for an empty recipient set: %v", stub.gotAddrs)
	}
}
