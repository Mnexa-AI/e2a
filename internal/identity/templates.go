package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// --- Template resource (user email templates, beta) ---
//
// A Template is one row in the templates table (migration 050): a reusable
// subject + plain-text body (+ optional HTML part) whose {{variable}}
// placeholders are rendered server-side at send time. The storage layer
// stores template SOURCE verbatim — syntax validation (internal/emailtemplate
// Parse) is the handler layer's job, mirroring how webhook filter validation
// lives above the store.
//
// Templates are owned by a user; cross-user reads return ErrTemplateNotFound
// to avoid leaking existence (same convention as webhooks/conversations).

// Template is one row in the templates table. Alias and HTMLBody use "" for
// SQL NULL (no alias / no HTML part) — the write path stores NULL via
// nullIfEmpty so the partial unique index on (user_id, alias) never sees
// empty-string collisions.
type Template struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Alias    string `json:"alias,omitempty"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	HTMLBody string `json:"html_body,omitempty"`
	// FromStarterAlias/FromStarterVersion record which starter master (and
	// at what catalog version) the template was copied from at create time;
	// "" (SQL NULL) for literal creates. Read-only provenance — edits don't
	// clear it.
	FromStarterAlias   string    `json:"from_starter_alias,omitempty"`
	FromStarterVersion string    `json:"from_starter_version,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Sentinel errors so API handlers can map error → HTTP status with
// errors.Is rather than string-matching.
var (
	ErrTemplateNotFound     = errors.New("template not found")
	ErrTemplateAliasTaken   = errors.New("template alias already in use")
	ErrTemplateLimitReached = errors.New("template count limit reached for this user")
)

// generateTemplateID produces the prefixed template ID via the store's
// shared generateID helper (crypto/rand, panics on OS RNG failure).
func generateTemplateID() string {
	return "tmpl_" + generateID()
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) — the alias-collision signal on the partial
// unique index idx_templates_user_alias.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

// TemplateCreate carries the fields of a new template. Alias, HTMLBody and
// the FromStarter* provenance pair may be "" (stored as SQL NULL).
type TemplateCreate struct {
	Name     string
	Alias    string
	Subject  string
	Body     string
	HTMLBody string
	// FromStarterAlias/FromStarterVersion are set only when the template is
	// copied from a starter master (from_starter on the create endpoint).
	FromStarterAlias   string
	FromStarterVersion string
}

// CreateTemplate inserts a new template. Returns ErrTemplateLimitReached at
// the per-user cap and ErrTemplateAliasTaken on a per-user alias collision.
//
// Syntax validation (Parse of all three parts) is the handler's job; the
// storage layer only enforces the count cap and alias uniqueness. The cap
// check races like the webhooks one (bounded overshoot of one row under
// concurrent creates) — acceptable at a cap of 10.
func (s *Store) CreateTemplate(ctx context.Context, userID string, in TemplateCreate) (*Template, error) {
	max, err := s.MaxTemplatesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	count, err := s.CountTemplatesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if count >= max {
		return nil, ErrTemplateLimitReached
	}

	now := time.Now()
	tp := &Template{
		ID:                 generateTemplateID(),
		UserID:             userID,
		Name:               in.Name,
		Alias:              in.Alias,
		Subject:            in.Subject,
		Body:               in.Body,
		HTMLBody:           in.HTMLBody,
		FromStarterAlias:   in.FromStarterAlias,
		FromStarterVersion: in.FromStarterVersion,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO templates (id, user_id, name, alias, subject, body, html_body, from_starter_alias, from_starter_version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tp.ID, tp.UserID, tp.Name, nullIfEmpty(tp.Alias), tp.Subject, tp.Body, nullIfEmpty(tp.HTMLBody),
		nullIfEmpty(tp.FromStarterAlias), nullIfEmpty(tp.FromStarterVersion), tp.CreatedAt, tp.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTemplateAliasTaken
		}
		return nil, fmt.Errorf("insert template: %w", err)
	}
	return tp, nil
}

const templateSelectColumns = `id, user_id, name, COALESCE(alias, ''), subject, body, COALESCE(html_body, ''), COALESCE(from_starter_alias, ''), COALESCE(from_starter_version, ''), created_at, updated_at`

func scanTemplate(row pgx.Row) (*Template, error) {
	tp := &Template{}
	err := row.Scan(&tp.ID, &tp.UserID, &tp.Name, &tp.Alias, &tp.Subject, &tp.Body, &tp.HTMLBody,
		&tp.FromStarterAlias, &tp.FromStarterVersion, &tp.CreatedAt, &tp.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		return nil, err
	}
	return tp, nil
}

// GetTemplateByID returns the template iff it's owned by userID. Cross-user
// reads (or missing rows) return ErrTemplateNotFound.
func (s *Store) GetTemplateByID(ctx context.Context, templateID, userID string) (*Template, error) {
	return scanTemplate(s.pool.QueryRow(ctx,
		`SELECT `+templateSelectColumns+` FROM templates WHERE id = $1 AND user_id = $2`,
		templateID, userID,
	))
}

// GetTemplateByAlias resolves a template by its per-user alias. Missing or
// cross-user aliases return ErrTemplateNotFound.
func (s *Store) GetTemplateByAlias(ctx context.Context, alias, userID string) (*Template, error) {
	return scanTemplate(s.pool.QueryRow(ctx,
		`SELECT `+templateSelectColumns+` FROM templates WHERE alias = $1 AND user_id = $2`,
		alias, userID,
	))
}

// TemplateSummary is the list-shape row: metadata only, no body sources.
// The list SELECT skips body/html_body on purpose — a full library of
// maximal templates would otherwise ship megabytes per list call, and every
// list consumer only needs metadata (starter-templates list/detail
// precedent). Fetch by id for the sources.
type TemplateSummary struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Alias     string    `json:"alias,omitempty"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListTemplatesByUser returns every template owned by the user, newest
// first — summaries only (no body/html_body).
func (s *Store) ListTemplatesByUser(ctx context.Context, userID string) ([]TemplateSummary, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, COALESCE(alias, ''), subject, created_at, updated_at
		 FROM templates WHERE user_id = $1 ORDER BY created_at DESC, id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TemplateSummary
	for rows.Next() {
		var tp TemplateSummary
		if err := rows.Scan(&tp.ID, &tp.UserID, &tp.Name, &tp.Alias, &tp.Subject, &tp.CreatedAt, &tp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, tp)
	}
	return out, rows.Err()
}

// CountTemplatesByUser returns the number of templates the user owns. Used
// by CreateTemplate to enforce the per-user cap.
func (s *Store) CountTemplatesByUser(ctx context.Context, userID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM templates WHERE user_id = $1`, userID,
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// DefaultMaxTemplates is the per-user cap fallback for users without an
// account_limits row — mirrors the column DEFAULT in migration 050, same
// pattern as DefaultMaxWebhooks.
const DefaultMaxTemplates = 10

// MaxTemplatesForUser returns the per-user cap from account_limits, or
// DefaultMaxTemplates when the user has no row.
func (s *Store) MaxTemplatesForUser(ctx context.Context, userID string) (int, error) {
	var n *int
	err := s.pool.QueryRow(ctx,
		`SELECT max_templates FROM account_limits WHERE user_id = $1`, userID,
	).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultMaxTemplates, nil
		}
		return 0, err
	}
	if n == nil {
		return DefaultMaxTemplates, nil
	}
	return *n, nil
}

// TemplateUpdate carries the fields a PATCH can change. All fields are
// pointers so handlers can distinguish "set to X" (including clearing the
// alias or HTML part with an empty string, stored as NULL) from "leave
// unchanged".
type TemplateUpdate struct {
	Name     *string
	Alias    *string
	Subject  *string
	Body     *string
	HTMLBody *string
}

// UpdateTemplate applies a partial update to a template owned by the user.
// Only non-nil fields are touched; updated_at is always bumped. Returns the
// updated row, ErrTemplateNotFound for missing/cross-user rows, and
// ErrTemplateAliasTaken on an alias collision.
func (s *Store) UpdateTemplate(ctx context.Context, templateID, userID string, u TemplateUpdate) (*Template, error) {
	args := []interface{}{templateID, userID}
	sets := []string{}
	add := func(col string, val interface{}) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if u.Name != nil {
		add("name", *u.Name)
	}
	if u.Alias != nil {
		add("alias", nullIfEmpty(*u.Alias))
	}
	if u.Subject != nil {
		add("subject", *u.Subject)
	}
	if u.Body != nil {
		add("body", *u.Body)
	}
	if u.HTMLBody != nil {
		add("html_body", nullIfEmpty(*u.HTMLBody))
	}

	if len(sets) == 0 {
		// No-op PATCH. Return the current row.
		return s.GetTemplateByID(ctx, templateID, userID)
	}
	sets = append(sets, "updated_at = now()")

	query := fmt.Sprintf(
		`UPDATE templates SET %s WHERE id = $1 AND user_id = $2 RETURNING id`,
		joinComma(sets),
	)
	var returnedID string
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&returnedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrTemplateAliasTaken
		}
		return nil, err
	}
	return s.GetTemplateByID(ctx, templateID, userID)
}

// DeleteTemplate removes a template owned by the user. Deleting a missing
// or cross-user template returns ErrTemplateNotFound, never silent success.
func (s *Store) DeleteTemplate(ctx context.Context, templateID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM templates WHERE id = $1 AND user_id = $2`,
		templateID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTemplateNotFound
	}
	return nil
}
