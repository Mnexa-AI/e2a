package piguard

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// DefaultScanCapBytes bounds how much decoded text the extractor will inspect. A
// multipart bomb or a huge attachment must never OOM the relay; content beyond the
// cap is dropped and DecodedSignals.Truncated is set so the caller can fail-to-review
// rather than treat "no finding" as safe. See design §5.
const DefaultScanCapBytes = 1 << 20 // 1 MiB of extracted text

// maxMIMEParts bounds part traversal independently of byte size — a deeply nested or
// part-flooded message stops walking here.
const maxMIMEParts = 200

// Extract decodes a raw RFC-2822 message into normalized Segments and the cheap
// DecodedSignals, inspecting at most capBytes of decoded text (DefaultScanCapBytes
// when capBytes <= 0). It never returns an error for malformed MIME — it extracts
// what it can and reports nothing for the rest — because adversarial input is the
// norm here; a parse failure must not crash or skip screening. The only returned
// error is for a fundamentally unreadable header block.
func Extract(raw []byte, capBytes int) ([]Segment, DecodedSignals, error) {
	if capBytes <= 0 {
		capBytes = DefaultScanCapBytes
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		// Unparseable header block: fall back to treating the whole blob as plain
		// text (still screen it) rather than failing open.
		seg := []Segment{{Type: SegmentTextPlain, Content: capString(string(raw), capBytes), Ref: "raw"}}
		sig := computeSignals(seg)
		sig.Truncated = len(raw) > capBytes
		return seg, sig, nil
	}

	acc := &extractor{cap: capBytes}
	if subj := decodeHeader(msg.Header.Get("Subject")); subj != "" {
		acc.add(SegmentSubject, subj, "subject")
	}
	// Sender/recipient display names are agent-visible ("who emailed me") and a known
	// injection carrier, so scan them too.
	for _, h := range []string{"From", "Reply-To", "To"} {
		if name := displayName(msg.Header.Get(h)); name != "" {
			acc.add(SegmentSubject, name, strings.ToLower(h))
		}
	}
	ct := msg.Header.Get("Content-Type")
	acc.walk(ct, msg.Header.Get("Content-Transfer-Encoding"), msg.Body, "body", 0)

	segs := acc.segments
	sig := computeSignals(segs)
	sig.Truncated = acc.full
	sig.Unscannable = acc.unscannable
	return segs, sig, nil
}

// extractor accumulates segments under a shared byte budget.
type extractor struct {
	segments    []Segment
	cap         int
	used        int
	parts       int
	full        bool // budget exhausted; Truncated will be set
	unscannable bool // a non-text part could not be scanned -> caller fails-to-review
}

func (e *extractor) add(t SegmentType, content, ref string) {
	if e.full || content == "" {
		return
	}
	remaining := e.cap - e.used
	if remaining <= 0 {
		e.full = true
		return
	}
	if len(content) > remaining {
		content = content[:remaining]
		e.full = true
	}
	e.used += len(content)
	e.segments = append(e.segments, Segment{Type: t, Content: content, Ref: ref})
}

// walk descends one MIME node. For multipart it recurses into each part; for leaf
// text parts it decodes the transfer encoding and routes to the right segment type.
func (e *extractor) walk(contentType, encoding string, body io.Reader, ref string, depth int) {
	if e.full {
		return
	}
	if depth > 20 {
		e.full = true // bailed on depth → report Truncated so the caller fails-to-review
		return
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			if e.full {
				return
			}
			part, err := mr.NextPart()
			if err != nil {
				return // io.EOF or malformed boundary — stop, keep what we have
			}
			e.parts++
			if e.parts > maxMIMEParts {
				e.full = true
				return
			}
			pCT := part.Header.Get("Content-Type")
			if pCT == "" {
				pCT = "text/plain"
			}
			disp := part.Header.Get("Content-Disposition")
			e.walkPart(pCT, part.Header.Get("Content-Transfer-Encoding"), disp, part, ref, depth+1)
			_ = part.Close()
		}
	}

	// Leaf node at the top level (non-multipart message).
	e.leaf(mediaType, encoding, params["charset"], "", body, ref)
}

// walkPart handles one multipart child: recurse if nested multipart, else treat as a
// leaf (decoding transfer encoding and honoring Content-Disposition).
func (e *extractor) walkPart(contentType, encoding, disposition string, body io.Reader, ref string, depth int) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		// Re-wrap: walk handles the multipart container itself.
		e.walkMultipart(params["boundary"], body, ref, depth)
		return
	}
	// Forwarded mail (message/rfc822) is the most common indirect-injection carrier —
	// recurse into the embedded message rather than treat it as an opaque blob.
	if mediaType == "message/rfc822" {
		e.walkRFC822(encoding, body, ref, depth)
		return
	}
	e.leaf(mediaType, encoding, params["charset"], disposition, body, ref)
}

func (e *extractor) walkRFC822(encoding string, body io.Reader, ref string, depth int) {
	if e.full {
		return
	}
	if depth > 20 {
		e.full = true
		return
	}
	remaining := e.cap - e.used
	if remaining <= 0 {
		e.full = true
		return
	}
	raw := decodeTransfer(readCapped(body, remaining+1), encoding)
	inner, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		e.add(SegmentTextPlain, raw, ref+"/rfc822")
		return
	}
	if subj := decodeHeader(inner.Header.Get("Subject")); subj != "" {
		e.add(SegmentSubject, subj, ref+"/rfc822-subject")
	}
	e.walk(inner.Header.Get("Content-Type"), inner.Header.Get("Content-Transfer-Encoding"), inner.Body, ref+"/rfc822", depth+1)
}

func (e *extractor) walkMultipart(boundary string, body io.Reader, ref string, depth int) {
	if e.full || boundary == "" {
		return
	}
	if depth > 20 {
		e.full = true // bailed on depth → report Truncated so the caller fails-to-review
		return
	}
	mr := multipart.NewReader(body, boundary)
	for {
		if e.full {
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		e.parts++
		if e.parts > maxMIMEParts {
			e.full = true
			return
		}
		e.walkPart(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"),
			part.Header.Get("Content-Disposition"), part, ref, depth+1)
		_ = part.Close()
	}
}

// leaf decodes and routes a non-multipart part. The declared Content-Type is
// attacker-controlled, so a part declared non-text is still inspected: its bytes
// are scanned when they look textual, else flagged unscannable.
func (e *extractor) leaf(mediaType, encoding, charset, disposition string, body io.Reader, ref string) {
	if e.full {
		return
	}
	isAttachment := strings.HasPrefix(strings.ToLower(strings.TrimSpace(disposition)), "attachment")
	remaining := e.cap - e.used
	if remaining <= 0 {
		e.full = true
		return
	}
	// Read at most remaining+1 bytes of the (still-encoded) part to bound memory and
	// to detect that there was more content than we keep. Let add() do the capping —
	// don't set e.full before add() or add() drops the final (truncated) chunk.
	raw := readCapped(body, remaining+1)
	overflow := len(raw) > remaining
	decoded := decodeCharset(decodeTransfer(raw, encoding), charset)

	if !strings.HasPrefix(mediaType, "text/") {
		// Non-text by declaration. Trust the bytes, not the label: scan textual content
		// (a payload mislabeled image/png, a secret in octet-stream); mark genuinely
		// binary content unscannable so the engine routes it to review (no OCR in v1,
		// and "we couldn't read it" is not a safety guarantee).
		if LooksTextual(decoded) {
			e.add(SegmentAttachmentText, decoded, ref+"/attachment")
		} else if strings.TrimSpace(decoded) != "" {
			e.unscannable = true
		}
		if overflow {
			e.full = true
		}
		return
	}

	switch {
	case isAttachment:
		e.add(SegmentAttachmentText, decoded, ref+"/attachment")
	case mediaType == "text/html":
		visible, hidden := splitHTML(decoded)
		e.add(SegmentHTMLVisible, visible, ref+"/html")
		if hidden != "" {
			e.add(SegmentHTMLHidden, hidden, ref+"/html#hidden")
		}
	default: // text/plain and other text/*
		e.add(SegmentTextPlain, decoded, ref+"/plain")
	}
	if overflow {
		e.full = true
	}
}

func readCapped(r io.Reader, limit int) string {
	if limit <= 0 {
		limit = 1
	}
	b, _ := io.ReadAll(io.LimitReader(r, int64(limit)))
	return string(b)
}

func decodeTransfer(s, encoding string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		// Be lenient: strip whitespace, tolerate missing padding.
		clean := strings.NewReplacer("\r", "", "\n", "", " ", "", "\t", "").Replace(s)
		if dec, err := base64.StdEncoding.DecodeString(clean); err == nil {
			return string(dec)
		}
		if dec, err := base64.RawStdEncoding.DecodeString(clean); err == nil {
			return string(dec)
		}
		return s
	case "quoted-printable":
		if dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(s))); err == nil {
			return string(dec)
		}
		return s
	default: // 7bit, 8bit, binary, empty
		return s
	}
}

// decodeHeader decodes RFC 2047 encoded-words (=?utf-8?...?=) in a header value,
// falling back to the raw value on error.
func decodeHeader(v string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(v); err == nil {
		return out
	}
	return v
}

func capString(s string, capBytes int) string {
	if len(s) > capBytes {
		return s[:capBytes]
	}
	return s
}

// --- HTML visible/hidden splitting ---

var hiddenStyleMarkers = []string{
	"display:none", "visibility:hidden", "opacity:0", "mso-hide:all",
	"font-size:0", "font:0", "line-height:0", "width:0", "height:0",
	"max-height:0", "text-indent:-", "clip:rect(0", "left:-9999", "top:-9999",
	// white-on-white is a common attack; color:white alone is a heuristic
	// (some false positives on dark backgrounds), but it feeds a *signal*, not a
	// hard block, so erring toward visibility is acceptable.
	"color:#fff", "color:#ffffff", "color:white", "color:rgb(255,255,255)",
}

// splitHTML returns (visibleText, hiddenText). It uses a small tag-aware scanner
// (not a full HTML parser — dependency-free by design) that tracks hidden-styled
// element depth. Crucially, content a human never sees but an LLM may still ingest —
// HTML comments, <script>/<style> bodies, and text behind a malformed/unterminated
// tag — is routed into the HIDDEN bucket (not discarded), since that is exactly where
// indirect-injection payloads hide. A full HTML parser is a possible future upgrade.
func splitHTML(html string) (visible, hidden string) {
	var vis, hid strings.Builder
	hiddenDepth := 0
	emit := func(text string) {
		text = decodeEntities(text)
		if hiddenDepth > 0 {
			hid.WriteString(text)
			hid.WriteByte(' ')
		} else {
			vis.WriteString(text)
			vis.WriteByte(' ')
		}
	}
	emitHidden := func(text string) {
		text = decodeEntities(text)
		hid.WriteString(text)
		hid.WriteByte(' ')
	}

	i, n := 0, len(html)
	for i < n {
		if html[i] != '<' {
			j := strings.IndexByte(html[i:], '<')
			if j < 0 {
				emit(html[i:])
				break
			}
			emit(html[i : i+j])
			i += j
			continue
		}
		// HTML comment: body is invisible to humans → screen it as hidden.
		if strings.HasPrefix(html[i:], "<!--") {
			rest := html[i+4:]
			if k := strings.Index(rest, "-->"); k >= 0 {
				emitHidden(rest[:k])
				i += 4 + k + 3
			} else {
				emitHidden(rest) // unterminated comment: the rest is the body
				break
			}
			continue
		}
		end := strings.IndexByte(html[i:], '>')
		if end < 0 {
			// Unterminated tag: don't drop the tail — a payload can hide behind a
			// broken tag. Treat the remaining text as hidden content.
			emitHidden(html[i+1:])
			break
		}
		tag := html[i+1 : i+end]
		i += end + 1
		tagLower := strings.ToLower(strings.TrimSpace(tag))
		name := tagName(tagLower)

		// <script>/<style> bodies are not rendered to the human → capture as hidden
		// and screen them rather than discarding.
		if name == "script" || name == "style" {
			if !strings.HasSuffix(tagLower, "/") {
				if body, past, ok := sliceClose(html[i:], name); ok {
					emitHidden(body)
					i += past
				} else {
					emitHidden(html[i:]) // no close tag — rest is the body
					i = n
				}
			}
			continue
		}

		switch {
		case strings.HasPrefix(tagLower, "/"):
			if hiddenDepth > 0 {
				hiddenDepth--
			}
		case strings.HasSuffix(tagLower, "/"), isVoidElement(name):
			// self-closing or void element (<br>, <img>, …): no content, no close,
			// so it must not change hidden depth (else following text mis-buckets).
		default:
			// Open a hidden subtree if the tag is hidden-styled, or keep tracking
			// nesting depth while already inside one so the matching close pops it.
			if tagIsHidden(tag) || hiddenDepth > 0 {
				hiddenDepth++
			}
		}
	}
	return normalizeSpace(vis.String()), normalizeSpace(hid.String())
}

func tagName(tagLower string) string {
	t := strings.TrimPrefix(tagLower, "/")
	for i := 0; i < len(t); i++ {
		if t[i] == ' ' || t[i] == '\t' || t[i] == '\n' || t[i] == '/' {
			return t[:i]
		}
	}
	return t
}

// sliceClose returns the body before the matching </name> and the index just past
// it. ok is false when no closing tag is found.
func sliceClose(s, name string) (body string, past int, ok bool) {
	needle := "</" + name
	idx := strings.Index(strings.ToLower(s), needle)
	if idx < 0 {
		return "", 0, false
	}
	gt := strings.IndexByte(s[idx:], '>')
	if gt < 0 {
		return "", 0, false
	}
	return s[:idx], idx + gt + 1, true
}

// voidElements are HTML elements that never have a closing tag.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

func isVoidElement(name string) bool { return voidElements[name] }

var styleAttrRe = regexp.MustCompile(`(?is)style\s*=\s*["']([^"']*)["']`)

func tagIsHidden(tag string) bool {
	tl := strings.ToLower(tag)
	// hidden / aria-hidden attributes.
	if strings.Contains(tl, " hidden") || strings.HasSuffix(tl, " hidden") {
		return true
	}
	if strings.Contains(tl, `aria-hidden="true"`) || strings.Contains(tl, "aria-hidden='true'") {
		return true
	}
	m := styleAttrRe.FindStringSubmatch(tag)
	if m == nil {
		return false
	}
	style := strings.ReplaceAll(strings.ToLower(m[1]), " ", "")
	for _, marker := range hiddenStyleMarkers {
		if strings.Contains(style, marker) {
			return true
		}
	}
	return false
}

var entityReplacer = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
	"&#39;", "'", "&apos;", "'", "&nbsp;", " ",
)

var numEntityRe = regexp.MustCompile(`&#(x[0-9a-fA-F]+|[0-9]+);`)

func decodeEntities(s string) string {
	s = entityReplacer.Replace(s)
	if !strings.Contains(s, "&#") {
		return s
	}
	// Numeric/hex character references (&#105; / &#x69;) render to humans but defeat
	// keyword matching unless decoded.
	return numEntityRe.ReplaceAllStringFunc(s, func(m string) string {
		digits := m[2 : len(m)-1]
		var code int64
		var err error
		if digits[0] == 'x' || digits[0] == 'X' {
			code, err = strconv.ParseInt(digits[1:], 16, 32)
		} else {
			code, err = strconv.ParseInt(digits, 10, 32)
		}
		if err != nil || code <= 0 || code > 0x10FFFF {
			return m
		}
		return string(rune(code))
	})
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// --- Signal computation ---

var (
	zeroWidthRunes = map[rune]bool{
		'\u200B': true, // zero-width space
		'\u200C': true, // zero-width non-joiner
		'\u200D': true, // zero-width joiner
		'\u2060': true, // word joiner
		'\uFEFF': true, // zero-width no-break space / BOM
	}
	fragmentJoinRe  = regexp.MustCompile(`(?is)(join|concat|combine|reassemble|piece together).{0,40}(http|url|link|string|character)`)
	fragmentProtoRe = regexp.MustCompile(`(?is)["']\s*h\s*["']\s*[,+]\s*["']\s*t+p?`)
)

func computeSignals(segs []Segment) DecodedSignals {
	var sig DecodedSignals
	var all strings.Builder
	var plain, htmlVis strings.Builder
	for _, s := range segs {
		all.WriteString(s.Content)
		all.WriteByte('\n')
		switch s.Type {
		case SegmentTextPlain:
			plain.WriteString(s.Content)
			plain.WriteByte(' ')
		case SegmentHTMLVisible:
			htmlVis.WriteString(s.Content)
			htmlVis.WriteByte(' ')
		case SegmentHTMLHidden:
			sig.HiddenCSSText = true
		}
	}
	text := all.String()

	var letters, homoglyphs int
	for _, r := range text {
		if zeroWidthRunes[r] {
			sig.ZeroWidth = true
		}
		if r >= 0xE0000 && r <= 0xE007F {
			sig.UnicodeTags = true
		}
		if unicode.IsLetter(r) {
			letters++
			if isHomoglyph(r) {
				homoglyphs++
			}
		}
	}
	if letters > 0 {
		sig.HomoglyphRatio = float64(homoglyphs) / float64(letters)
	}
	if fragmentJoinRe.MatchString(text) || fragmentProtoRe.MatchString(text) {
		sig.FragmentedURL = true
	}
	sig.PlainHTMLDiverge = textsDiverge(plain.String(), htmlVis.String())
	return sig
}

// isHomoglyph reports whether r is a non-ASCII letter from a block commonly used to
// spoof Latin characters (Cyrillic, Greek).
func isHomoglyph(r rune) bool {
	if r < 0x80 {
		return false
	}
	switch {
	case r >= 0x0400 && r <= 0x04FF: // Cyrillic
		return true
	case r >= 0x0370 && r <= 0x03FF: // Greek
		return true
	default:
		return false
	}
}

// textsDiverge reports whether two non-trivial texts share little vocabulary
// (Jaccard < 0.5 over lowercased word sets). Empty/near-empty inputs never diverge.
func textsDiverge(a, b string) bool {
	wa := wordSet(a)
	wb := wordSet(b)
	if len(wa) < 3 || len(wb) < 3 {
		return false
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	union := len(wa) + len(wb) - inter
	if union == 0 {
		return false
	}
	return float64(inter)/float64(union) < 0.5
}

func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(w) >= 2 {
			set[w] = true
		}
	}
	return set
}

// decodeCharset transcodes a part body from its declared charset to UTF-8 so the
// regex/Unicode signals see real text. UTF-16/UTF-7 and legacy 8-bit charsets
// otherwise defeat every \b-anchored pattern. Unknown / UTF-8 charsets pass through.
func decodeCharset(s, charset string) string {
	cs := strings.ToLower(strings.TrimSpace(charset))
	if cs == "" || cs == "utf-8" || cs == "utf8" || cs == "us-ascii" || cs == "ascii" {
		return s
	}
	enc, err := htmlindex.Get(cs)
	if err != nil {
		return s
	}
	out, _, err := transform.String(enc.NewDecoder(), s)
	if err != nil || out == "" {
		return s
	}
	return out
}

// LooksTextual reports whether a byte string is predominantly text (a low ratio of
// binary control bytes). Used to decide whether a part whose declared Content-Type
// is non-text actually carries scannable text — the declared type is attacker-
// controlled and not trusted.
func LooksTextual(s string) bool {
	if len(s) == 0 {
		return false
	}
	ctrl := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b < 0x20 && b != '\t' && b != '\n' && b != '\r') || b == 0x7f {
			ctrl++
		}
	}
	return float64(ctrl)/float64(len(s)) < 0.10
}

// displayName returns the human display-name portion of an address header
// ("Alice <a@b>" -> "Alice"), RFC-2047 decoded. Empty when there is no name.
func displayName(header string) string {
	if header == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(header); err == nil {
		return strings.TrimSpace(addr.Name)
	}
	h := decodeHeader(header)
	if i := strings.IndexByte(h, '<'); i > 0 {
		return strings.TrimSpace(h[:i])
	}
	return ""
}
