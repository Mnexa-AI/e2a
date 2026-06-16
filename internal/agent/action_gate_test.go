package agent

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/actiongate"
	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// Slice 7b — the trust-gated hold decision. White-box tests of actionGateHold
// mapping a referenced inbound's DMARC verdict + participants to a hold/send
// decision under each hitl_mode. (The pure predicate is covered in
// internal/actiongate; this pins the e2a-specific signal extraction.)

func refMsg(t *testing.T, dmarc emailauth.CheckStatus, sender string, to, cc []string) *identity.Message {
	t.Helper()
	return &identity.Message{
		Sender:       sender,
		ToRecipients: to,
		CC:           cc,
		Auth:         &emailauth.Result{DMARC: emailauth.CheckResult{Status: dmarc}},
	}
}

func agentWith(mode string) *identity.AgentIdentity {
	a := &identity.AgentIdentity{ID: "bot@acme.com", Domain: "acme.com", HITLEnabled: true, HITLMode: mode}
	return a
}

func TestActionGateHold_HighImpactMode(t *testing.T) {
	ag := agentWith(actiongate.ModeHighImpact)
	// Referenced inbound from a spoofable sender (dmarc fail), thread participants
	// are all @acme.com.
	weak := refMsg(t, emailauth.StatusFail, "boss@acme.com", []string{"bot@acme.com"}, nil)
	strong := refMsg(t, emailauth.StatusPass, "boss@acme.com", []string{"bot@acme.com"}, nil)

	cases := []struct {
		name string
		ref  *identity.Message
		req  outbound.SendRequest
		hold bool
	}{
		{"weak verdict + forward to third party → hold", weak, outbound.SendRequest{To: []string{"legal@external.com"}}, true},
		{"weak verdict + reply within thread → send", weak, outbound.SendRequest{To: []string{"boss@acme.com"}}, false},
		{"weak verdict + reply to new same-domain → send", weak, outbound.SendRequest{To: []string{"hr@acme.com"}}, false},
		{"strong verdict + forward to third party → send", strong, outbound.SendRequest{To: []string{"legal@external.com"}}, false},
		{"weak verdict + cc adds external → hold", weak, outbound.SendRequest{To: []string{"boss@acme.com"}, CC: []string{"x@evil.com"}}, true},
		{"cold send (no referenced) → send", nil, outbound.SendRequest{To: []string{"anyone@wherever.com"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := actionGateHold(ag, c.req, c.ref).Hold; got != c.hold {
				t.Errorf("Hold = %v, want %v", got, c.hold)
			}
		})
	}
}

// TestActionGateHold_AllMode: hold-all is unchanged — every outbound is held,
// even a trusted in-thread reply or a cold send.
func TestActionGateHold_AllMode(t *testing.T) {
	ag := agentWith(actiongate.ModeAll)
	strong := refMsg(t, emailauth.StatusPass, "boss@acme.com", []string{"bot@acme.com"}, nil)
	if !actionGateHold(ag, outbound.SendRequest{To: []string{"boss@acme.com"}}, strong).Hold {
		t.Error("all mode must hold a trusted in-thread reply")
	}
	if !actionGateHold(ag, outbound.SendRequest{To: []string{"x@acme.com"}}, nil).Hold {
		t.Error("all mode must hold a cold send")
	}
}

// TestActionGateHold_EmptyModeDefaultsAll: a blank hitl_mode (pre-migration
// rows COALESCE to 'all') behaves as hold-all.
func TestActionGateHold_EmptyModeDefaultsAll(t *testing.T) {
	ag := agentWith("")
	if !actionGateHold(ag, outbound.SendRequest{To: []string{"boss@acme.com"}}, nil).Hold {
		t.Error("empty hitl_mode must default to hold-all")
	}
}

// TestActionGateHold_MissingVerdictIsUntrusted: a referenced inbound with no
// stored auth verdict is treated as untrusted (fail-closed) → high-impact held.
func TestActionGateHold_MissingVerdictIsUntrusted(t *testing.T) {
	ag := agentWith(actiongate.ModeHighImpact)
	noVerdict := &identity.Message{Sender: "boss@acme.com", ToRecipients: []string{"bot@acme.com"}} // Auth == nil
	if !actionGateHold(ag, outbound.SendRequest{To: []string{"legal@external.com"}}, noVerdict).Hold {
		t.Error("missing verdict must be treated as untrusted (fail-closed) and held when high-impact")
	}
}
