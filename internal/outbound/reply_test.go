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
