package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- Webhook resource (top-level webhooks-as-a-resource feature) ---
//
// A Webhook is one subscriber row in the new /api/v1/webhooks resource.
// It is owned by a user (cross-user reads return ErrWebhookNotFound to
// avoid leaking existence), can subscribe to one or more event types,
// and applies scope filters (agent_ids, conversation_ids, labels) to
// further narrow which events fire to it.
//
// The legacy agent_identities.webhook_url field continues to work
// unchanged in slice 1; the publisher fires events to both pathways
// side-by-side. See the final design at tmp/e2a_webhooks_design.md for
// the full feature scope.

// WebhookFilters is the structured form of webhooks.filters JSONB.
// Empty / nil slices mean "no constraint of that type" — a webhook
// with all-empty filters is a cross-cutting subscriber that matches
// every event of the right type for the owning user.
type WebhookFilters struct {
	AgentIDs        []string `json:"agent_ids,omitempty"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
	Labels          []string `json:"labels,omitempty"`
}

// Webhook is one row in the webhooks table.
//
// SigningSecret carries the plaintext secret. It's populated on
// CreateWebhook responses (the caller's one chance to see the secret)
// and read by the delivery worker when signing the X-E2A-Signature
// header. Public API GET endpoints in slice 2 will scrub this field
// before responding so a stolen API key cannot exfiltrate webhook
// secrets via list/get.
//
// SigningSecretPrev + SigningSecretPrevExpiresAt hold the previous
// secret during the 24h rotation grace window; slice 4 dual-signs
// using both during that window so receivers can roll forward.
type Webhook struct {
	ID                          string          `json:"id"`
	UserID                      string          `json:"user_id"`
	URL                         string          `json:"url"`
	Description                 string          `json:"description"`
	Events                      []string        `json:"events"`
	Filters                     WebhookFilters  `json:"filters"`
	SigningSecret               string          `json:"signing_secret,omitempty"`
	SigningSecretPrev           string          `json:"-"`
	SigningSecretPrevExpiresAt  *time.Time      `json:"-"`
	Enabled                     bool            `json:"enabled"`
	AutoDisabledAt              *time.Time      `json:"auto_disabled_at,omitempty"`
	CreatedAt                   time.Time       `json:"created_at"`
	LastDeliveredAt             *time.Time      `json:"last_delivered_at,omitempty"`
}

// Sentinel errors so API handlers can map error → HTTP status with
// errors.Is rather than string-matching.
var (
	ErrWebhookNotFound    = errors.New("webhook not found")
	ErrWebhookCapReached  = errors.New("webhook count limit reached for this user")
)

// generateWebhookID and generateWebhookSecret produce the prefixed IDs
// and secrets used by the webhooks API. Both use crypto/rand and
// panic on OS RNG failure — same pattern as generateID +
// generateAPIKey in store.go (an all-zero secret is catastrophic).
func generateWebhookID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	return "wh_" + hex.EncodeToString(b)
}

// generateWebhookSecret returns a prefixed secret of the form whsec_<64-hex>.
// The whsec_ prefix matches Stripe's convention so secret-scanning tools
// (GitGuardian, GitHub secret scanning, etc.) can recognize the format.
// 32 bytes of entropy is plenty for HMAC-SHA256 keying.
func generateWebhookSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	return "whsec_" + hex.EncodeToString(b)
}

// CreateWebhook inserts a new row and returns it with the plaintext
// signing secret populated. The plaintext is only available on this
// response — subsequent GET/list calls scrub it.
//
// Filters validation (charset, count caps, agent ownership) is the
// handler's job in slice 2; the storage layer only verifies the
// per-user count cap from account_limits.max_webhooks.
func (s *Store) CreateWebhook(ctx context.Context, userID, url, description string, events []string, filters WebhookFilters) (*Webhook, error) {
	// Enforce the per-user cap before generating any state. Race
	// across concurrent creates is bounded by the cap + 1 in the
	// worst case; an exact race-free check would need SELECT FOR
	// UPDATE on a sentinel row, which is not worth it for a cap of
	// 50. The race-window cost is "one user briefly has 51 webhooks"
	// — acceptable.
	max, err := s.MaxWebhooksForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	count, err := s.CountWebhooksByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if count >= max {
		return nil, ErrWebhookCapReached
	}

	filtersJSON, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("marshal filters: %w", err)
	}

	w := &Webhook{
		ID:            generateWebhookID(),
		UserID:        userID,
		URL:           url,
		Description:   description,
		Events:        events,
		Filters:       filters,
		SigningSecret: generateWebhookSecret(),
		Enabled:       true,
		CreatedAt:     time.Now(),
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO webhooks (id, user_id, url, description, events, filters, signing_secret, enabled, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		w.ID, w.UserID, w.URL, w.Description, w.Events, filtersJSON, w.SigningSecret, w.Enabled, w.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert webhook: %w", err)
	}
	return w, nil
}

// GetWebhookByID returns the webhook iff it's owned by userID. Cross-
// user reads (or missing rows) return ErrWebhookNotFound — same
// not-found-on-cross-user convention used elsewhere in the codebase
// (conversation reads, message reads).
//
// The returned Webhook has SigningSecret populated for the delivery
// worker's benefit; the public API layer scrubs this field before
// responding to GETs.
func (s *Store) GetWebhookByID(ctx context.Context, webhookID, userID string) (*Webhook, error) {
	w := &Webhook{}
	var filtersJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, url, description, events, filters,
		        signing_secret, COALESCE(signing_secret_prev, ''),
		        signing_secret_prev_expires_at,
		        enabled, auto_disabled_at, created_at, last_delivered_at
		 FROM webhooks WHERE id = $1 AND user_id = $2`,
		webhookID, userID,
	).Scan(
		&w.ID, &w.UserID, &w.URL, &w.Description, &w.Events, &filtersJSON,
		&w.SigningSecret, &w.SigningSecretPrev,
		&w.SigningSecretPrevExpiresAt,
		&w.Enabled, &w.AutoDisabledAt, &w.CreatedAt, &w.LastDeliveredAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(filtersJSON, &w.Filters); err != nil {
		return nil, fmt.Errorf("unmarshal filters: %w", err)
	}
	return w, nil
}

// GetWebhookByIDInternal returns the webhook by ID with no ownership
// check. INTERNAL USE ONLY — handler code MUST use GetWebhookByID
// which scopes by user_id. The retry worker uses this to look up the
// URL + signing secret for a delivery row whose ownership was already
// established when the publisher inserted it.
//
// The suffix Internal mirrors the convention in dkim.GetDKIMKeyInternal:
// a method name that calls out "skipping the standard authorization
// check" so a reviewer doesn't have to read the body to know why.
func (s *Store) GetWebhookByIDInternal(ctx context.Context, webhookID string) (*Webhook, error) {
	w := &Webhook{}
	var filtersJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, url, description, events, filters,
		        signing_secret, COALESCE(signing_secret_prev, ''),
		        signing_secret_prev_expires_at,
		        enabled, auto_disabled_at, created_at, last_delivered_at
		 FROM webhooks WHERE id = $1`,
		webhookID,
	).Scan(
		&w.ID, &w.UserID, &w.URL, &w.Description, &w.Events, &filtersJSON,
		&w.SigningSecret, &w.SigningSecretPrev,
		&w.SigningSecretPrevExpiresAt,
		&w.Enabled, &w.AutoDisabledAt, &w.CreatedAt, &w.LastDeliveredAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(filtersJSON, &w.Filters); err != nil {
		return nil, fmt.Errorf("unmarshal filters: %w", err)
	}
	return w, nil
}

// ListWebhooksByUser returns every webhook owned by the user — used
// by the slice-2 GET /api/v1/webhooks endpoint. Storage layer surfaces
// enabled and disabled rows alike; filter at the handler if needed.
func (s *Store) ListWebhooksByUser(ctx context.Context, userID string) ([]Webhook, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, url, description, events, filters,
		        signing_secret, COALESCE(signing_secret_prev, ''),
		        signing_secret_prev_expires_at,
		        enabled, auto_disabled_at, created_at, last_delivered_at
		 FROM webhooks WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var w Webhook
		var filtersJSON []byte
		if err := rows.Scan(
			&w.ID, &w.UserID, &w.URL, &w.Description, &w.Events, &filtersJSON,
			&w.SigningSecret, &w.SigningSecretPrev,
			&w.SigningSecretPrevExpiresAt,
			&w.Enabled, &w.AutoDisabledAt, &w.CreatedAt, &w.LastDeliveredAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(filtersJSON, &w.Filters); err != nil {
			return nil, fmt.Errorf("unmarshal filters: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListEnabledWebhooksForRouting is the hot-path query used by the
// event publisher. Returns enabled webhooks for the user that
// subscribe to the given event type. The in-process publisher then
// applies filter matching in Go (cheaper than encoding the
// AND-across-types + OR-within-type rule in SQL at slice-1 scale).
//
// The partial index idx_webhooks_user_enabled WHERE enabled = true
// keeps this O(log n) on the common case (a user has a small number
// of enabled webhooks).
func (s *Store) ListEnabledWebhooksForRouting(ctx context.Context, userID, eventType string) ([]Webhook, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, url, description, events, filters,
		        signing_secret, COALESCE(signing_secret_prev, ''),
		        signing_secret_prev_expires_at,
		        enabled, auto_disabled_at, created_at, last_delivered_at
		 FROM webhooks
		 WHERE user_id = $1 AND enabled = true AND $2 = ANY(events)`,
		userID, eventType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var w Webhook
		var filtersJSON []byte
		if err := rows.Scan(
			&w.ID, &w.UserID, &w.URL, &w.Description, &w.Events, &filtersJSON,
			&w.SigningSecret, &w.SigningSecretPrev,
			&w.SigningSecretPrevExpiresAt,
			&w.Enabled, &w.AutoDisabledAt, &w.CreatedAt, &w.LastDeliveredAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(filtersJSON, &w.Filters); err != nil {
			return nil, fmt.Errorf("unmarshal filters: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CountWebhooksByUser returns the total number of webhooks (enabled +
// disabled) the user owns. Used by CreateWebhook to enforce the
// per-user cap from account_limits.max_webhooks.
func (s *Store) CountWebhooksByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM webhooks WHERE user_id = $1`, userID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// MaxWebhooksForUser returns the per-user cap from account_limits.
// Users without an account_limits row default to 50 — the column
// DEFAULT, mirrored here as a fallback so the cap works on dev
// installs that haven't seeded an account_limits row.
const DefaultMaxWebhooks = 50

func (s *Store) MaxWebhooksForUser(ctx context.Context, userID string) (int, error) {
	var n *int
	err := s.pool.QueryRow(ctx,
		`SELECT max_webhooks FROM account_limits WHERE user_id = $1`, userID,
	).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultMaxWebhooks, nil
		}
		return 0, err
	}
	if n == nil {
		return DefaultMaxWebhooks, nil
	}
	return *n, nil
}

// DeleteWebhook removes a webhook owned by the user. Idempotent:
// deleting a non-existent or cross-user webhook returns
// ErrWebhookNotFound, never silently succeeds. The ON DELETE CASCADE
// on webhook_subscriber_deliveries.webhook_id drops pending delivery
// rows automatically — no separate cleanup needed.
//
// Slice 1 includes this method (rather than deferring to slice 2's
// handler work) because tests need it for setup teardown and the
// implementation is trivial.
func (s *Store) DeleteWebhook(ctx context.Context, webhookID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webhooks WHERE id = $1 AND user_id = $2`,
		webhookID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWebhookNotFound
	}
	return nil
}
