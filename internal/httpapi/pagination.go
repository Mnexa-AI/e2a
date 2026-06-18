package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum number of items to return (1-100)."`
}

// ErrInvalidCursor is returned when a cursor fails to decode. Handlers map
// it to a 400 with code "invalid_cursor".
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// EncodeCursor serializes an arbitrary cursor payload (the position +
// filter snapshot a resource needs to resume) into the opaque, URL-safe
// string clients echo back. The payload shape is private to each resource;
// clients must treat the cursor as opaque.
func EncodeCursor(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeCursor reverses EncodeCursor into dst. A malformed cursor yields
// ErrInvalidCursor rather than a generic JSON error so callers branch on a
// stable sentinel. An empty cursor is treated as "start from the
// beginning" — dst is left untouched and nil is returned.
func DecodeCursor(cursor string, dst any) error {
	if cursor == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return ErrInvalidCursor
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrInvalidCursor
	}
	return nil
}
