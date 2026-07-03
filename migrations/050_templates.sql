-- 050_templates.sql
--
-- User-owned email templates (beta). A template is a reusable subject +
-- body (+ optional HTML part) with {{variable}} placeholders, rendered
-- server-side at send time (POST /v1/agents/{email}/messages with
-- template_id/template_alias + template_data). Rendering happens BEFORE
-- the HITL hold so reviewers always see the final content.
--
-- Decisions reflected here:
-- - id is app-generated ("tmpl_" + 32 hex from crypto/rand), matching the
--   {type}_{random} convention (webhooks: wh_...).
-- - alias is an optional human-friendly handle, unique per user when set.
--   The partial unique index enforces per-user uniqueness only for rows
--   that HAVE an alias — unnamed templates don't collide on NULL.
-- - body is the plain-text part (NOT NULL: every email needs a text part);
--   html_body is the optional HTML part.
-- - template SOURCE is stored verbatim; parse-time validation lives in the
--   handler layer (internal/emailtemplate), not in a CHECK constraint.
--
-- account_limits.max_templates mirrors max_webhooks (migration 024): a
-- per-user cap on template count, read by the store's create path.
-- ALTER TABLE ... ADD COLUMN ... DEFAULT 10 is metadata-only on
-- Postgres 11+ (constant default) and account_limits is small regardless.
--
-- Idempotent: CREATE TABLE / CREATE INDEX / ADD COLUMN all use IF NOT
-- EXISTS. Additive only — no destructive ALTERs.

CREATE TABLE IF NOT EXISTS templates (
    id         TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL CHECK (name <> ''),
    alias      TEXT,
    subject    TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    html_body  TEXT,
    -- Starter provenance: which catalog master (and at what version) this
    -- template was copied from via from_starter; NULL for literal creates.
    -- Recorded once at create time — later edits don't clear it.
    from_starter_alias   TEXT,
    from_starter_version TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Belt-and-braces for databases that applied an earlier draft of this
-- migration before the provenance columns existed (the tracker skips
-- already-applied files, so the CREATE TABLE above won't re-run there).
ALTER TABLE templates
    ADD COLUMN IF NOT EXISTS from_starter_alias TEXT;
ALTER TABLE templates
    ADD COLUMN IF NOT EXISTS from_starter_version TEXT;

-- Per-user alias uniqueness, only for rows that set one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_templates_user_alias
    ON templates (user_id, alias) WHERE alias IS NOT NULL;

-- List/count path: templates are always read per-owner.
CREATE INDEX IF NOT EXISTS idx_templates_user
    ON templates (user_id);

ALTER TABLE account_limits
    ADD COLUMN IF NOT EXISTS max_templates INTEGER NOT NULL DEFAULT 10;
