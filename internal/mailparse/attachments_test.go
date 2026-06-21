package mailparse

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// A multipart/mixed message: text body + a base64 PDF attachment + a named
// inline image. The two named parts are attachments; the text body is not.
func sampleMultipart() []byte {
	return []byte("From: a@x.com\r\n" +
		"To: b@y.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"the body text\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/pdf; name=\"report.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" + b64("%PDF-1.4 fake pdf bytes") + "\r\n" +
		"--BOUND\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" + b64("\x89PNG\r\n fake png") + "\r\n" +
		"--BOUND--\r\n")
}

func TestAttachments_OrderAndDecode(t *testing.T) {
	atts := Attachments(sampleMultipart())
	if len(atts) != 2 {
		t.Fatalf("want 2 attachments (text body excluded), got %d: %+v", len(atts), atts)
	}
	if atts[0].Filename != "report.pdf" || atts[0].ContentType != "application/pdf" {
		t.Errorf("att0 meta wrong: %+v", atts[0])
	}
	if string(atts[0].Data) != "%PDF-1.4 fake pdf bytes" {
		t.Errorf("att0 bytes not decoded: %q", atts[0].Data)
	}
	if atts[1].Filename != "logo.png" || atts[1].ContentType != "image/png" {
		t.Errorf("att1 meta wrong: %+v", atts[1])
	}
	if !bytes.Equal(atts[1].Data, []byte("\x89PNG\r\n fake png")) {
		t.Errorf("att1 binary bytes not decoded: %q", atts[1].Data)
	}
}

func TestAttachmentAt_Bounds(t *testing.T) {
	raw := sampleMultipart()
	if _, ok := AttachmentAt(raw, 0); !ok {
		t.Error("index 0 should exist")
	}
	if a, ok := AttachmentAt(raw, 1); !ok || a.Filename != "logo.png" {
		t.Errorf("index 1 should be logo.png, got ok=%v %+v", ok, a)
	}
	if _, ok := AttachmentAt(raw, 2); ok {
		t.Error("index 2 is out of range, want ok=false")
	}
	if _, ok := AttachmentAt(raw, -1); ok {
		t.Error("negative index, want ok=false")
	}
}

func TestAttachments_NoneOnPlainMessage(t *testing.T) {
	plain := []byte("From: a@x.com\r\nSubject: hi\r\nContent-Type: text/plain\r\n\r\njust text, no attachments\r\n")
	if atts := Attachments(plain); len(atts) != 0 {
		t.Errorf("plain message should have no attachments, got %d", len(atts))
	}
}

func TestAttachments_QuotedPrintable(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain; name=\"note.txt\"\r\n" +
		"Content-Disposition: attachment; filename=\"note.txt\"\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\nca=C3=A9\r\n" + // "caé"
		"--B--\r\n")
	atts := Attachments(raw)
	if len(atts) != 1 || string(atts[0].Data) != "caé" {
		t.Fatalf("quoted-printable attachment decode failed: %+v", atts)
	}
}

func TestAttachments_MalformedReturnsEmpty(t *testing.T) {
	if atts := Attachments([]byte("not a real \x00 mime message")); atts != nil && len(atts) != 0 {
		t.Errorf("malformed input should yield no attachments, got %+v", atts)
	}
}
