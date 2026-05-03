package outbound

import (
	"reflect"
	"testing"
)

func buildRawMessage(headers map[string]string, body string) []byte {
	var msg string
	for k, v := range headers {
		msg += k + ": " + v + "\r\n"
	}
	msg += "\r\n" + body
	return []byte(msg)
}

func TestParseReplyRecipients_BasicReply(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From":    "alice@gmail.com",
		"To":      "agent@example.com",
		"Subject": "Hello",
	}, "body")

	r, err := ParseReplyRecipients(raw, false, nil)
	if err != nil {
		t.Fatalf("ParseReplyRecipients: %v", err)
	}
	if !reflect.DeepEqual(r.To, []string{"alice@gmail.com"}) {
		t.Errorf("To = %v, want [alice@gmail.com]", r.To)
	}
	if len(r.CC) != 0 {
		t.Errorf("CC = %v, want empty", r.CC)
	}
}

func TestParseReplyRecipients_UsesReplyTo(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From":     "alice@gmail.com",
		"Reply-To": "support@company.com",
		"To":       "agent@example.com",
	}, "body")

	r, _ := ParseReplyRecipients(raw, false, nil)
	if !reflect.DeepEqual(r.To, []string{"support@company.com"}) {
		t.Errorf("To = %v, want [support@company.com]", r.To)
	}
}

func TestParseReplyRecipients_MultipleReplyTo(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From":     "alice@gmail.com",
		"Reply-To": "support@company.com, ceo@company.com",
		"To":       "agent@example.com",
	}, "body")

	r, _ := ParseReplyRecipients(raw, false, nil)
	want := []string{"support@company.com", "ceo@company.com"}
	if !reflect.DeepEqual(r.To, want) {
		t.Errorf("To = %v, want %v", r.To, want)
	}
}

func TestParseReplyRecipients_ReplyAll(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From": "alice@gmail.com",
		"To":   "agent@example.com, bob@gmail.com",
		"Cc":   "carol@gmail.com",
	}, "body")

	r, _ := ParseReplyRecipients(raw, true, nil)
	if !reflect.DeepEqual(r.To, []string{"alice@gmail.com"}) {
		t.Errorf("To = %v, want [alice@gmail.com]", r.To)
	}
	wantCC := []string{"agent@example.com", "bob@gmail.com", "carol@gmail.com"}
	if !reflect.DeepEqual(r.CC, wantCC) {
		t.Errorf("CC = %v, want %v", r.CC, wantCC)
	}
}

func TestParseReplyRecipients_ReplyAllWithExtraCC(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From": "alice@gmail.com",
		"To":   "agent@example.com",
	}, "body")

	r, _ := ParseReplyRecipients(raw, true, []string{"extra@gmail.com"})
	wantCC := []string{"agent@example.com", "extra@gmail.com"}
	if !reflect.DeepEqual(r.CC, wantCC) {
		t.Errorf("CC = %v, want %v", r.CC, wantCC)
	}
}

func TestParseReplyRecipients_NoRawMessage_ReplyAll_Fails(t *testing.T) {
	_, err := ParseReplyRecipients(nil, true, []string{"extra@gmail.com"})
	if err == nil {
		t.Error("expected error for reply_all with no raw message")
	}
}

func TestParseReplyRecipients_NoRawMessage_NonReplyAll_OK(t *testing.T) {
	r, err := ParseReplyRecipients(nil, false, []string{"extra@gmail.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.To) != 0 {
		t.Errorf("To = %v, want empty", r.To)
	}
	if !reflect.DeepEqual(r.CC, []string{"extra@gmail.com"}) {
		t.Errorf("CC = %v, want [extra@gmail.com]", r.CC)
	}
}

func TestParseReplyRecipients_NotReplyAllIgnoresOriginalRecipients(t *testing.T) {
	raw := buildRawMessage(map[string]string{
		"From": "alice@gmail.com",
		"To":   "agent@example.com, bob@gmail.com",
		"Cc":   "carol@gmail.com",
	}, "body")

	r, _ := ParseReplyRecipients(raw, false, nil)
	if !reflect.DeepEqual(r.To, []string{"alice@gmail.com"}) {
		t.Errorf("To = %v, want [alice@gmail.com]", r.To)
	}
	if len(r.CC) != 0 {
		t.Errorf("CC = %v, want empty (not reply-all)", r.CC)
	}
}

// --- BuildReferencesChain ---
//
// Threading correctness is the load-bearing test: in a multi-party email
// thread, a recipient that was not on every prior message must still be
// able to anchor the reply via the References chain. These tests cover
// the spec behavior in RFC 5322 § 3.6.4 and the specific multi-party
// scheduler scenario that motivated the fix.

func TestBuildReferencesChain_NoParent(t *testing.T) {
	got := BuildReferencesChain([]byte{}, "")
	if got != nil {
		t.Errorf("got %v, want nil for empty parent", got)
	}
}

func TestBuildReferencesChain_NoRawMessage(t *testing.T) {
	// No raw message but we have the parent's Message-ID — chain is just [parent].
	got := BuildReferencesChain(nil, "<parent@host>")
	want := []string{"<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_ParentHasReferences(t *testing.T) {
	// Spec case: parent has References → new chain = parent.References ++ [parent.MessageID].
	raw := buildRawMessage(map[string]string{
		"References": "<u1@host> <a1@host>",
	}, "body")

	got := BuildReferencesChain(raw, "<parent@host>")
	want := []string{"<u1@host>", "<a1@host>", "<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_ParentHasInReplyToOnly(t *testing.T) {
	// Spec case: parent has only In-Reply-To → use it as the prior chain.
	raw := buildRawMessage(map[string]string{
		"In-Reply-To": "<u1@host>",
	}, "body")

	got := BuildReferencesChain(raw, "<parent@host>")
	want := []string{"<u1@host>", "<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_ParentHasNeither(t *testing.T) {
	// Top of thread: no References, no In-Reply-To → chain = [parent].
	raw := buildRawMessage(map[string]string{
		"From":    "alice@gmail.com",
		"Subject": "Hi",
	}, "body")

	got := BuildReferencesChain(raw, "<parent@host>")
	want := []string{"<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_DropsDuplicateParent(t *testing.T) {
	// If the parent's own Message-ID was already in its References (some
	// clients write it that way), the resulting chain must not contain
	// duplicates — the parent appears exactly once, at the end.
	raw := buildRawMessage(map[string]string{
		"References": "<u1@host> <parent@host> <a1@host>",
	}, "body")

	got := BuildReferencesChain(raw, "<parent@host>")
	want := []string{"<u1@host>", "<a1@host>", "<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_MalformedRaw(t *testing.T) {
	// Garbage raw message must not blow up the send. Fall back to [parent].
	got := BuildReferencesChain([]byte("not an RFC 2822 message at all"), "<parent@host>")
	want := []string{"<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildReferencesChain_MultilineFolded(t *testing.T) {
	// References headers commonly span multiple lines via RFC 5322 folding
	// (CRLF + WSP). net/mail unfolds these; we just need the IDs back.
	raw := []byte("References: <u1@host>\r\n <a1@host>\r\n <a2@host>\r\nSubject: Hi\r\n\r\nbody")

	got := BuildReferencesChain(raw, "<parent@host>")
	want := []string{"<u1@host>", "<a1@host>", "<a2@host>", "<parent@host>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// MultiPartyThreadFork is the regression case for the bug this PR fixes.
//
// Scenario: a Gmail user (U) emails an agent (A); A replies with cc=B
// where B is another agent. B replies back to A (only) via e2a — that
// reply's Message-ID never enters U's mailbox. Then A replies-all with
// the booking confirmation. With the old behavior, In-Reply-To pointed
// at B's Message-ID, which U has never seen, and Gmail forked the thread.
// With the chain, References still contains <u1>, so Gmail can anchor.
func TestBuildReferencesChain_MultiPartyThreadFork(t *testing.T) {
	// B → A (the inbound A is now responding to). B's References chain
	// already includes the original user message and A's first outbound.
	bToA := buildRawMessage(map[string]string{
		"From":       "b@e2a.dev",
		"To":         "a@e2a.dev",
		"Subject":    "Re: Hello",
		"References": "<u1@gmail> <a1@e2a>",
	}, "body")

	// A is now sending a reply. Its parent Message-ID is B's reply.
	got := BuildReferencesChain(bToA, "<b1@e2a>")

	// The chain MUST contain <u1@gmail> — that is the only Message-ID
	// the original user U has in their mailbox. Without it, U's Gmail
	// can't thread A's reply against the existing conversation.
	foundU := false
	for _, id := range got {
		if id == "<u1@gmail>" {
			foundU = true
			break
		}
	}
	if !foundU {
		t.Errorf("regression: References chain %v missing <u1@gmail> — Gmail will fork the thread for the original user", got)
	}

	want := []string{"<u1@gmail>", "<a1@e2a>", "<b1@e2a>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
