package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Page is the one list-response shape across every v1 collection
// (api-v1-redesign §4 decision 7): an items array plus an opaque
// continuation cursor that is null on the last page.
//
//	{ "items": [...], "next_cursor": "…" | null }
//
// It is generic over the item view type so each resource reuses the same
// envelope without redefining it.
type Page[T any] struct {
	Items      []T     `json:"items" nullable:"false"`
	NextCursor *string `json:"next_cursor"`
}

// NewPage builds a Page. A nil/empty nextCursor renders as JSON null,
// signalling "no more pages"; items is normalized to a non-nil empty slice
// so the field is always `[]`, never `null`.
func NewPage[T any](items []T, nextCursor string) Page[T] {
	if items == nil {
		items = []T{}
	}
	p := Page[T]{Items: items}
	if nextCursor != "" {
		p.NextCursor = &nextCursor
	}
	return p
}

// PageParams is the embeddable Huma input fragment for cursor pagination.
// Every list operation embeds it so `cursor` + `limit` are declared, typed,
// and validated identically across the surface.
type PageParams struct {
	Cursor string `query:"cursor" doc:"Opaque pagination cursor from a previous response's next_cursor. Continuation requests must not change the other filters."`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"100" doc:"Maximum number of items to return (1-100)."`
}

// ErrInvalidCursor is returned when a cursor fails to decode. Handlers map
// it to a 400 with code "invalid_cursor".
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// keysetCursor is the standard opaque continuation for a collection
// keyset-paginated on (created_at, id) with no cursor-pinned filters — the
// common case across the account-scoped lists (agents, domains, webhooks,
// webhook deliveries, templates, api keys, reviews). The compact json keys keep
// the encoded cursor short. Resources that must pin extra filters across a
// continuation (messages, events) define their own richer cursor instead.
//
// `id` holds whatever unique tiebreak the resource's ORDER BY uses — the row id
// for most, the domain string for the domains list (its unique key).
type keysetCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

// decodeKeyset resolves a (created_at, id) continuation cursor into its keyset
// position. An empty cursor is the first page (zero time, empty id). A malformed
// or tampered cursor yields a 400 invalid_cursor envelope.
func (s *Server) decodeKeyset(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	var cur keysetCursor
	if err := DecodeCursor([]string{s.deps.CursorSecret}, cursor, &cur); err != nil {
		return time.Time{}, "", NewError(http.StatusBadRequest, "invalid_cursor", "invalid pagination cursor")
	}
	return cur.CreatedAt, cur.ID, nil
}

// encodeKeyset mints the next-page cursor from the last row's (created_at, id).
// A marshal failure maps to a 500 envelope (matches the other list handlers).
func (s *Server) encodeKeyset(createdAt time.Time, id string) (string, error) {
	c, err := EncodeCursor(s.deps.CursorSecret, keysetCursor{CreatedAt: createdAt, ID: id})
	if err != nil {
		return "", NewError(http.StatusInternalServerError, "internal_error", "failed to build pagination cursor")
	}
	return c, nil
}

// effectiveLimit normalizes a request limit to the default when unset (<=0).
// Mirrors the inline `if limit <= 0 { limit = 100 }` the list handlers share.
func effectiveLimit(limit int) int {
	if limit <= 0 {
		return defaultPageLimit
	}
	return limit
}

// defaultPageLimit is the page size when a list request omits limit — the same
// default PageParams declares (100).
const defaultPageLimit = 100

// EncodeCursor serializes an arbitrary cursor payload (the position +
// filter snapshot a resource needs to resume) into the opaque, URL-safe,
// tamper-evident string clients echo back.
//
// The cursor is HMAC-signed (issue #144, finding M2): a client can no
// longer decode the base64, edit a field, and re-encode it — any such edit
// breaks the signature and DecodeCursor rejects it. The cursor remains
// opaque to clients; the payload shape stays private to each resource.
//
// Format:
//
//	base64url(json_payload) + "." + base64url(hmac_sha256(secret, base64url(json_payload)))
//
// The MAC is computed over the LITERAL emitted base64url(json) segment (not
// a re-marshaled struct) so encode and verify are byte-canonical and cannot
// drift. secret is the deployment HMAC secret (config.Signing.HMACSecret) —
// the same key approvaltoken and the X-E2A-Auth-* email headers already use,
// so there is no new key to manage.
func EncodeCursor(secret string, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	sig := cursorMAC([]byte(secret), []byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// DecodeCursor verifies a cursor's HMAC and reverses it into dst. A
// malformed, tampered, or wrong-secret cursor yields ErrInvalidCursor
// rather than a generic error so callers branch on a stable sentinel. An
// empty cursor is treated as "start from the beginning" — dst is left
// untouched and nil is returned.
//
// secrets is tried in order and the signature is compared in constant time
// (hmac.Equal, mirroring approvaltoken.Verify). Accepting a slice supports
// HMAC-secret rotation: a cursor signed under an old secret keeps verifying
// until that secret is retired. Today callers pass a single-element slice.
//
// Old unsigned cursors (plain base64url(json) with no "." signature segment,
// as emitted before issue #144 M2) no longer verify and are hard-rejected
// with ErrInvalidCursor. Cursors are ephemeral, so a client mid-pagination
// simply restarts the query.
func DecodeCursor(secrets []string, cursor string, dst any) error {
	if cursor == "" {
		return nil
	}
	parts := strings.SplitN(cursor, ".", 2)
	if len(parts) != 2 {
		return ErrInvalidCursor
	}
	providedSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidCursor
	}
	matched := false
	for _, secret := range secrets {
		if hmac.Equal(providedSig, cursorMAC([]byte(secret), []byte(parts[0]))) {
			matched = true
			break
		}
	}
	if !matched {
		return ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidCursor
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrInvalidCursor
	}
	return nil
}

// cursorMAC computes HMAC-SHA256 of payload under secret. Mirrors
// approvaltoken.signMAC so the two signing paths stay convention-identical.
func cursorMAC(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}
