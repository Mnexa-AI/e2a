// Package mailparse produces the injection-reduced "parsed view" of an inbound
// email (decision 9 / Slice 4b-3): the text an agent feeds to a model by
// default, derived server-side from the raw RFC 5322 message — prefer the
// text/plain part (else HTML→text), strip quoted reply chains / forwarded
// headers, and cap the length (a token-stuffing guard).
//
// It is a CONVENIENCE, not a security control: the raw message is always
// available, and the security decision is made on structured metadata (the
// auth verdict + sender provenance, which survive as fields), never on this
// stripped text. So over-stripping is acceptable — the caller can fall back to
// raw. Best-effort throughout: a parse failure yields the best text available
// (possibly empty), never an error.
package mailparse

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// DefaultMaxBytes caps the parsed text. Generous enough for real messages,
// small enough to blunt token-stuffing. Callers may override.
const DefaultMaxBytes = 16 * 1024

// maxMIMEDepth bounds multipart-nesting recursion. mime/multipart re-scans
// buffered data at each level, so an attacker-nested message is O(depth²) — a
// ~2MB email (under the 10MB inbound cap) could otherwise pin a request
// goroutine for minutes, and the parsed view is computed synchronously on every
// read. Real mail nests a handful of levels; past this we bail to best-effort
// (empty), consistent with the package's "over-stripping is acceptable" stance.
const maxMIMEDepth = 32

// ParsedBody extracts the best human-readable text from a raw RFC 5322 message,
// strips quoted reply/forward chains, and truncates to maxBytes. truncated is
// true when the cap cut content. maxBytes <= 0 uses DefaultMaxBytes.
func ParsedBody(raw []byte, maxBytes int) (text string, truncated bool) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	body := extractText(raw)
	body = stripQuotedReplies(body)
	body = normalizeWhitespace(body)
	if len(body) > maxBytes {
		// Truncate on a rune boundary.
		cut := maxBytes
		for cut > 0 && !utf8RuneStart(body[cut]) {
			cut--
		}
		return strings.TrimSpace(body[:cut]), true
	}
	return body, false
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// extractText walks the MIME tree and returns the best text representation:
// the first text/plain part, or (failing that) the first text/html rendered to
// text. Falls back to the raw body if MIME parsing fails.
func extractText(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	plain, htmlText := walkParts(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, 0)
	if plain != "" {
		return plain
	}
	if htmlText != "" {
		return htmlText
	}
	// No recognized text part — return the decoded top-level body.
	b, _ := io.ReadAll(msg.Body)
	return string(b)
}

// walkParts returns the first text/plain and first text/html-as-text found
// anywhere in the (possibly nested multipart) body.
func walkParts(contentType, cte string, body io.Reader, depth int) (plain, htmlText string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// No/!invalid Content-Type → treat as text/plain.
		return decode(body, cte), ""
	}
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		if depth >= maxMIMEDepth {
			return "", "" // bail on pathological nesting (see maxMIMEDepth)
		}
		boundary := params["boundary"]
		if boundary == "" {
			return "", ""
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			p, h := walkParts(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
			if plain == "" && p != "" {
				plain = p
			}
			if htmlText == "" && h != "" {
				htmlText = h
			}
			part.Close()
			if plain != "" {
				break // text/plain is preferred; stop early
			}
		}
		return plain, htmlText
	case mediaType == "text/plain":
		return decode(body, cte), ""
	case mediaType == "text/html":
		return "", htmlToText(decode(body, cte))
	default:
		return "", ""
	}
}

// decode reads a body applying its Content-Transfer-Encoding (base64 /
// quoted-printable / identity).
func decode(body io.Reader, cte string) string {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		b, _ := io.ReadAll(quotedprintable.NewReader(body))
		return string(b)
	case "base64":
		raw, _ := io.ReadAll(body)
		// base64 bodies are line-wrapped; strip all whitespace then decode.
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(raw))
		dec, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return string(raw)
		}
		return string(dec)
	default:
		b, _ := io.ReadAll(body)
		return string(b)
	}
}

// htmlToText renders HTML to plain text: drops script/style, inserts newlines
// at block boundaries, and decodes entities (via the tokenizer).
func htmlToText(h string) string {
	z := html.NewTokenizer(strings.NewReader(h))
	var sb strings.Builder
	skip := 0 // depth inside script/style
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return sb.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head":
				if tt == html.StartTagToken {
					skip++
				}
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
				sb.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head":
				if skip > 0 {
					skip--
				}
			case "p", "div", "tr", "li", "blockquote":
				sb.WriteByte('\n')
			}
		case html.TextToken:
			if skip == 0 {
				sb.Write(z.Text())
			}
		}
	}
}

// quoteMarker matches the start of a quoted reply / forwarded block. Anything
// from the first match onward is dropped.
var quoteMarker = regexp.MustCompile(`(?im)^\s*(` +
	`>.*$` + // a quoted line
	`|On .+ wrote:\s*$` + // Gmail/Apple "On <date>, <name> wrote:"
	`|-{2,}\s*Original Message\s*-{2,}\s*$` + // Outlook
	`|-{2,}\s*Forwarded message\s*-{2,}\s*$` + // forwarded
	`|_{10,}\s*$` + // Outlook divider line
	`|From:\s.+\sSent:\s` + // Outlook forwarded header (From: … Sent: …)
	`)`)

// stripQuotedReplies cuts the text at the first quoted-reply / forwarded marker
// and removes any trailing ">"-quoted lines. Conservative: only well-known
// markers trigger a cut (over-stripping is acceptable; raw is always available).
func stripQuotedReplies(text string) string {
	if loc := quoteMarker.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}
	return strings.TrimRight(text, " \t\r\n")
}

// normalizeWhitespace collapses runs of blank lines and trims trailing spaces
// per line, so the parsed view is compact without losing paragraph structure.
func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t\r")
		if ln == "" {
			blank++
			if blank > 1 {
				continue // collapse 2+ blank lines into one
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
