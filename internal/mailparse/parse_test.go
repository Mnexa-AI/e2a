package mailparse

import "strings"

import "testing"

func TestParsedBody(t *testing.T) {
	t.Run("plain text passes through", func(t *testing.T) {
		raw := "From: a@x.com\r\nTo: b@y.com\r\nSubject: Hi\r\nContent-Type: text/plain\r\n\r\nHello there.\r\n"
		got, trunc := ParsedBody([]byte(raw), 0)
		if got != "Hello there." || trunc {
			t.Fatalf("got %q trunc=%v", got, trunc)
		}
	})

	t.Run("html rendered to text", func(t *testing.T) {
		raw := "From: a@x.com\r\nContent-Type: text/html\r\n\r\n<html><body><p>Hello</p><script>evil()</script><p>World</p></body></html>"
		got, _ := ParsedBody([]byte(raw), 0)
		if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
			t.Fatalf("expected Hello/World, got %q", got)
		}
		if strings.Contains(got, "evil") {
			t.Fatalf("script content must be dropped, got %q", got)
		}
	})

	t.Run("multipart prefers text/plain", func(t *testing.T) {
		raw := "From: a@x.com\r\nContent-Type: multipart/alternative; boundary=B\r\n\r\n" +
			"--B\r\nContent-Type: text/plain\r\n\r\nPlain version.\r\n" +
			"--B\r\nContent-Type: text/html\r\n\r\n<p>HTML version</p>\r\n--B--\r\n"
		got, _ := ParsedBody([]byte(raw), 0)
		if got != "Plain version." {
			t.Fatalf("expected plain part, got %q", got)
		}
	})

	t.Run("quoted-printable decoded", func(t *testing.T) {
		raw := "From: a@x.com\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nCaf=C3=A9 time"
		got, _ := ParsedBody([]byte(raw), 0)
		if got != "Café time" {
			t.Fatalf("expected decoded café, got %q", got)
		}
	})

	t.Run("strips quoted reply (On ... wrote:)", func(t *testing.T) {
		raw := "From: a@x.com\r\nContent-Type: text/plain\r\n\r\nMy reply.\r\n\r\nOn Mon, Jan 1, 2026, Bob <b@y.com> wrote:\r\n> original\r\n> more original\r\n"
		got, _ := ParsedBody([]byte(raw), 0)
		if got != "My reply." {
			t.Fatalf("expected only the reply, got %q", got)
		}
	})

	t.Run("strips Outlook original-message block", func(t *testing.T) {
		raw := "From: a@x.com\r\nContent-Type: text/plain\r\n\r\nTop post.\r\n\r\n-----Original Message-----\r\nFrom: someone\r\nblah\r\n"
		got, _ := ParsedBody([]byte(raw), 0)
		if got != "Top post." {
			t.Fatalf("expected top post, got %q", got)
		}
	})

	t.Run("length cap truncates", func(t *testing.T) {
		body := strings.Repeat("a", 100)
		raw := "From: a@x.com\r\nContent-Type: text/plain\r\n\r\n" + body
		got, trunc := ParsedBody([]byte(raw), 20)
		if !trunc || len(got) > 20 {
			t.Fatalf("expected truncation to <=20, got len=%d trunc=%v", len(got), trunc)
		}
	})

	t.Run("malformed message degrades to raw, no panic", func(t *testing.T) {
		got, _ := ParsedBody([]byte("not a real email at all"), 0)
		if got == "" {
			t.Fatal("expected best-effort text, got empty")
		}
	})
}
