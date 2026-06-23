-- 048_workspaces_migration_a.sql
--
-- Workspaces (multi-user teams), Migration A — the additive, safe-on-prod
-- phase. See docs/design/2026-06-23-workspaces.md §4.1, §4.7, §4.8.
--
-- A workspace is the billed tenant: it owns agents, domains, keys, limits,
-- and usage. Individuals own nothing directly; access is membership. Every
-- existing user gets a personal workspace (deterministic id, backfilled
-- idempotently). Every workspace-owned table gains a workspace_id column,
-- backfilled from user_id → personal workspace, else → the ws_system
-- sentinel (so no row is left NULL).
--
-- This migration is ADDITIVE ONLY by design (blocker B1/B3): it CREATEs the
-- new tables, ADD COLUMNs workspace_id (nullable), backfills it, and flips
-- the small-table PK/UNIQUE constraints to workspace_id. It does NOT drop
-- the workspace-owned tables' user_id ON DELETE CASCADE FKs and does NOT
-- make workspace_id NOT NULL — those are deferred to Migration B
-- (049_workspaces_migration_b.sql) so deploy-1 rollback stays safe.
--
-- Identity-owned tables (user_sessions, oauth_*) are NOT re-keyed and KEEP
-- their user cascade — a credential/session must die with the human (B2).
--
-- Idempotent: CREATE TABLE/INDEX IF NOT EXISTS, ADD COLUMN IF NOT EXISTS,
-- ON CONFLICT DO NOTHING backfills, guarded constraint flips. Safe to rerun.
-- The deterministic personal-workspace id (ws_ + md5(user_id)) makes the
-- backfill a no-op on re-run.

-- pgcrypto (gen_random_bytes / digest) — already enabled by 004, kept here
-- so this file is self-contained on a fresh DB.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- 1. New tables
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS workspaces (
    id          TEXT PRIMARY KEY,                                  -- ws_…
    name        TEXT NOT NULL,
    -- created_by is an audit FK: ON DELETE SET NULL so deleting a user
    -- neither blocks on nor cascades through a surviving service key (§4.1).
    created_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    -- invited_by audit FK: SET NULL so a removed inviter doesn't cascade.
    invited_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

-- Membership/role lookups are hot only for session + OAuth auth (key auth
-- needs no read — role is constant 'member', workspace intrinsic). Index
-- (user_id) INCLUDE (role); the (workspace_id) index is redundant with the
-- PK (workspace_id, user_id). (§6)
CREATE INDEX IF NOT EXISTS idx_workspace_members_user
    ON workspace_members (user_id) INCLUDE (role);

CREATE TABLE IF NOT EXISTS workspace_invitations (
    id           TEXT PRIMARY KEY,                                 -- inv_…
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email        TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    token_hash   TEXT NOT NULL,                                    -- SHA256 of the bearer token (cf. api_keys)
    -- invited_by audit FK: SET NULL so a removed inviter doesn't cascade.
    invited_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    expires_at   TIMESTAMPTZ,
    accepted_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one pending invitation per (workspace, email). Re-invite upserts
-- the pending row; accepted/revoked/expired rows are excluded so the same
-- email can be re-invited after a prior invite resolves (§4.6).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_workspace_invitations_pending
    ON workspace_invitations (workspace_id, email)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_workspace_invitations_workspace
    ON workspace_invitations (workspace_id);

-- Admin-action audit log (§5). Invite / remove / role-change / rename leave
-- zero forensic trail under the "admins are peers" model; this table is
-- written in the same tx as each admin mutation. Small surface.
CREATE TABLE IF NOT EXISTS audit_log (
    id             TEXT PRIMARY KEY,                               -- aud_…
    workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    -- actor is an audit FK: SET NULL so deleting the actor user keeps the
    -- forensic row (who-did-what survives the human's deletion).
    actor_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    action         TEXT NOT NULL,                                  -- e.g. member.invited, member.removed, role.changed, workspace.renamed
    target         TEXT NOT NULL DEFAULT '',                       -- target user id / invitation id / free-form
    detail         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_workspace_created
    ON audit_log (workspace_id, created_at DESC);

-- last_active_workspace_id is an advisory UI convenience on the session
-- (§4.2) — never an authz input. Written conditionally so steady-state
-- requests do zero extra writes. user_sessions is identity-owned and is NOT
-- re-keyed; this is just an extra advisory column.
ALTER TABLE user_sessions
    ADD COLUMN IF NOT EXISTS last_active_workspace_id TEXT;

-- ---------------------------------------------------------------------------
-- 2. Seed the protected system/sentinel workspace (ws_system)
-- ---------------------------------------------------------------------------
-- Owns rows with no real user (B2): the seeded shared domain agents.e2a.dev
-- (user_id IS NULL) and any usage_events rows already NULLed by ON DELETE
-- SET NULL. Guarded against teardown in application code (§5).
INSERT INTO workspaces (id, name, created_by)
VALUES ('ws_system', 'System', NULL)
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 3. Backfill one personal workspace per existing user
-- ---------------------------------------------------------------------------
-- Deterministic id (ws_ + md5(user_id)) so re-runs are no-ops. Named
-- "{name}'s Workspace", falling back to the email local-part when name is
-- blank (mirrors §4.5's helper). The user is inserted as admin of it.
INSERT INTO workspaces (id, name, created_by, created_at)
SELECT
    'ws_' || md5(u.id),
    CASE
        WHEN btrim(u.name) <> '' THEN u.name || '''s Workspace'
        ELSE split_part(u.email, '@', 1) || '''s Workspace'
    END,
    u.id,
    u.created_at
FROM users u
ON CONFLICT (id) DO NOTHING;

-- Admin membership for each user in their personal workspace. ON CONFLICT on
-- the PK (workspace_id, user_id) keeps the re-run idempotent.
INSERT INTO workspace_members (workspace_id, user_id, role, invited_by, created_at)
SELECT 'ws_' || md5(u.id), u.id, 'admin', u.id, u.created_at
FROM users u
ON CONFLICT (workspace_id, user_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 4. ADD COLUMN workspace_id (nullable) on every workspace-owned table
-- ---------------------------------------------------------------------------
-- Per §4.1. messages is intentionally NOT given a column (owned via
-- agent_id → agent_identities.workspace_id); send_attempts likewise (owned
-- via message → agent). Identity-owned tables (user_sessions, oauth_*) are
-- NOT re-keyed.
ALTER TABLE domains                  ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE agent_identities         ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE api_keys                 ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE account_limits           ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE account_usage            ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE usage_events             ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE usage_summaries          ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE suppressions             ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE webhooks                 ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE webhook_events           ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE webhook_signing_secrets  ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE idempotency_keys         ADD COLUMN IF NOT EXISTS workspace_id TEXT;

-- api_keys also gains created_by (audit/revoke) — SET NULL on user delete so
-- a workspace service key survives the minter's deletion (§4.1, §4.3.1).
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS created_by TEXT REFERENCES users(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- 5. Backfill workspace_id (no row left NULL)
-- ---------------------------------------------------------------------------
-- Resolution: user_id → that user's personal workspace (ws_ + md5(user_id))
-- where the user exists; else → ws_system. agent_identities is backfilled
-- FIRST so the re-keyed storage trigger (step 6) can resolve workspace_id
-- from it. The large usage_events backfill is the additive ADD COLUMN above
-- plus a bounded sweep here; the bulk historical backfill runs as an
-- out-of-band resumable, idempotent, chunked script (WHERE workspace_id IS
-- NULL), not blocking this migration (§4.8 step 5).

-- agent_identities — user_id is NOT NULL, so every row resolves to a personal
-- workspace (or ws_system if the user somehow vanished). Done first.
UPDATE agent_identities
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(agent_identities.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

-- domains — user_id is nullable (the seeded shared domain is NULL → ws_system).
UPDATE domains
   SET workspace_id = CASE
       WHEN user_id IS NULL THEN 'ws_system'
       ELSE COALESCE(
           (SELECT id FROM workspaces WHERE id = 'ws_' || md5(domains.user_id)),
           'ws_system')
   END
 WHERE workspace_id IS NULL;

-- api_keys — user_id NOT NULL. Also backfill created_by from the minting user
-- (best available attribution for pre-existing keys).
UPDATE api_keys
   SET workspace_id = COALESCE(
           (SELECT id FROM workspaces WHERE id = 'ws_' || md5(api_keys.user_id)),
           'ws_system'),
       created_by = COALESCE(created_by, user_id)
 WHERE workspace_id IS NULL;

UPDATE account_limits
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(account_limits.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE account_usage
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(account_usage.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

-- usage_events — user_id is ON DELETE SET NULL, so some rows are already NULL
-- (B2): those resolve to ws_system. This UPDATE is the bounded sweep; on a
-- prod-sized table the bulk fill runs out-of-band (see header).
UPDATE usage_events
   SET workspace_id = CASE
       WHEN user_id IS NULL THEN 'ws_system'
       ELSE COALESCE(
           (SELECT id FROM workspaces WHERE id = 'ws_' || md5(usage_events.user_id)),
           'ws_system')
   END
 WHERE workspace_id IS NULL;

UPDATE usage_summaries
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(usage_summaries.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE suppressions
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(suppressions.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE webhooks
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(webhooks.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE webhook_events
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(webhook_events.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE webhook_signing_secrets
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(webhook_signing_secrets.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

UPDATE idempotency_keys
   SET workspace_id = COALESCE(
       (SELECT id FROM workspaces WHERE id = 'ws_' || md5(idempotency_keys.user_id)),
       'ws_system')
 WHERE workspace_id IS NULL;

-- workspace_id FK on every backfilled table → workspaces(id). Added here
-- (validated immediately, cheap on these small/just-backfilled tables) so a
-- workspace_id always points at a real workspace. usage_events / usage_summaries
-- get their FK in Migration B alongside the NOT NULL flip (the bulk backfill
-- runs out-of-band first). messages/send_attempts have no column.
DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE suppressions ADD CONSTRAINT suppressions_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE webhooks ADD CONSTRAINT webhooks_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE webhook_signing_secrets ADD CONSTRAINT webhook_signing_secrets_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- account_limits / account_usage / usage_summaries / idempotency_keys get
-- their workspace_id FK implicitly via the PK flip below (the new PK column
-- still needs an explicit FK for referential integrity); add them here.
DO $$ BEGIN
    ALTER TABLE account_limits ADD CONSTRAINT account_limits_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE account_usage ADD CONSTRAINT account_usage_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE usage_summaries ADD CONSTRAINT usage_summaries_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ---------------------------------------------------------------------------
-- 6. Constraint flips on the small tables (§4.8 step 6)
-- ---------------------------------------------------------------------------
-- Flip the primary/unique key from user-scoped to workspace-scoped. Cheap on
-- these small tables — NOT the messages/usage_events rewrite hazard. The
-- workspace_id columns are fully backfilled above (no NULLs on these tables),
-- so the new PK/UNIQUE will not violate.

-- account_limits PK: user_id → workspace_id. One row per workspace.
DO $$ BEGIN
    ALTER TABLE account_limits DROP CONSTRAINT account_limits_pkey;
EXCEPTION WHEN undefined_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE account_limits ADD CONSTRAINT account_limits_pkey
        PRIMARY KEY (workspace_id);
EXCEPTION WHEN invalid_table_definition THEN NULL; END $$;

-- account_usage PK: user_id → workspace_id. REQUIRED before the re-keyed
-- storage trigger's ON CONFLICT (workspace_id) below.
DO $$ BEGIN
    ALTER TABLE account_usage DROP CONSTRAINT account_usage_pkey;
EXCEPTION WHEN undefined_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE account_usage ADD CONSTRAINT account_usage_pkey
        PRIMARY KEY (workspace_id);
EXCEPTION WHEN invalid_table_definition THEN NULL; END $$;

-- usage_summaries PK: (user_id, bucket_date) → (workspace_id, bucket_date).
DO $$ BEGIN
    ALTER TABLE usage_summaries DROP CONSTRAINT usage_summaries_pkey;
EXCEPTION WHEN undefined_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE usage_summaries ADD CONSTRAINT usage_summaries_pkey
        PRIMARY KEY (workspace_id, bucket_date);
EXCEPTION WHEN invalid_table_definition THEN NULL; END $$;

-- idempotency_keys PK: (user_id, key) → (workspace_id, key). Dedup domain
-- widens to the workspace (§4.1) — a deliberate, documented choice.
DO $$ BEGIN
    ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
EXCEPTION WHEN undefined_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_pkey
        PRIMARY KEY (workspace_id, key);
EXCEPTION WHEN invalid_table_definition THEN NULL; END $$;

-- suppressions UNIQUE: (user_id, address) → (workspace_id, address). Else a
-- complaint from one member would leak across the workspace incorrectly /
-- cross-member; flip to per-workspace.
DO $$ BEGIN
    ALTER TABLE suppressions DROP CONSTRAINT suppressions_user_id_address_key;
EXCEPTION WHEN undefined_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE suppressions ADD CONSTRAINT suppressions_workspace_id_address_key
        UNIQUE (workspace_id, address);
EXCEPTION WHEN duplicate_table THEN NULL; END $$;

-- Drop the now-redundant NOT NULL on user_id for tables whose new
-- workspace-keyed writers no longer populate user_id. The storage trigger
-- (step 7) writes account_usage by workspace_id only; new account_limits /
-- usage_summaries / idempotency_keys rows are workspace-keyed. user_id is
-- retained for audit but must be nullable so workspace-only inserts succeed.
-- DROP NOT NULL is metadata-only (no rewrite) — safe on these small tables.
DO $$ BEGIN
    ALTER TABLE account_limits ALTER COLUMN user_id DROP NOT NULL;
EXCEPTION WHEN others THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE account_usage ALTER COLUMN user_id DROP NOT NULL;
EXCEPTION WHEN others THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE usage_summaries ALTER COLUMN user_id DROP NOT NULL;
EXCEPTION WHEN others THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE idempotency_keys ALTER COLUMN user_id DROP NOT NULL;
EXCEPTION WHEN others THEN NULL; END $$;

-- domains primary-per-tenant UNIQUE index: re-key user_id → workspace_id
-- (else two members each set a primary, §4.1). Drop the old partial unique
-- index from 013 and recreate on workspace_id.
DROP INDEX IF EXISTS uniq_domains_primary_per_user;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_domains_primary_per_workspace
    ON domains (workspace_id)
    WHERE is_primary = true AND workspace_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 7. Re-key the account_usage storage trigger (§4.7 — most dangerous step)
-- ---------------------------------------------------------------------------
-- account_usage is written ONLY by e2a_messages_storage_delta (039). It must
-- resolve+upsert by workspace_id now, in LOCKSTEP with the PK flip above
-- (ON CONFLICT (workspace_id) requires the new PK). The
-- "IF workspace_id IS NULL THEN RETURN NEW" guard is preserved so a
-- window-created agent whose workspace_id hasn't been backfilled yet does not
-- abort the message write (a delivery-breaking outage otherwise).
CREATE OR REPLACE FUNCTION e2a_messages_storage_delta() RETURNS TRIGGER AS $$
DECLARE
    wsid      TEXT;
    new_bytes BIGINT;
    old_bytes BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT workspace_id INTO wsid FROM agent_identities WHERE id = NEW.agent_id;
        IF wsid IS NULL THEN
            RETURN NEW;
        END IF;
        new_bytes := COALESCE(octet_length(NEW.raw_message), 0)
                   + COALESCE(octet_length(NEW.body_text), 0)
                   + COALESCE(octet_length(NEW.body_html), 0)
                   + COALESCE(octet_length(NEW.attachments_json::text), 0);
        INSERT INTO account_usage (workspace_id, storage_bytes)
        VALUES (wsid, new_bytes)
        ON CONFLICT (workspace_id) DO UPDATE
            SET storage_bytes = account_usage.storage_bytes + EXCLUDED.storage_bytes,
                updated_at    = now();
        RETURN NEW;

    ELSIF TG_OP = 'UPDATE' THEN
        -- agent_id never changes on a message UPDATE, so NEW.agent_id is correct.
        SELECT workspace_id INTO wsid FROM agent_identities WHERE id = NEW.agent_id;
        IF wsid IS NULL THEN
            RETURN NEW;
        END IF;
        new_bytes := COALESCE(octet_length(NEW.raw_message), 0)
                   + COALESCE(octet_length(NEW.body_text), 0)
                   + COALESCE(octet_length(NEW.body_html), 0)
                   + COALESCE(octet_length(NEW.attachments_json::text), 0);
        old_bytes := COALESCE(octet_length(OLD.raw_message), 0)
                   + COALESCE(octet_length(OLD.body_text), 0)
                   + COALESCE(octet_length(OLD.body_html), 0)
                   + COALESCE(octet_length(OLD.attachments_json::text), 0);
        IF new_bytes <> old_bytes THEN
            UPDATE account_usage
               SET storage_bytes = GREATEST(storage_bytes + (new_bytes - old_bytes), 0),
                   updated_at    = now()
             WHERE workspace_id = wsid;
        END IF;
        RETURN NEW;

    ELSIF TG_OP = 'DELETE' THEN
        SELECT workspace_id INTO wsid FROM agent_identities WHERE id = OLD.agent_id;
        IF wsid IS NULL THEN
            RETURN OLD;
        END IF;
        old_bytes := COALESCE(octet_length(OLD.raw_message), 0)
                   + COALESCE(octet_length(OLD.body_text), 0)
                   + COALESCE(octet_length(OLD.body_html), 0)
                   + COALESCE(octet_length(OLD.attachments_json::text), 0);
        UPDATE account_usage
           SET storage_bytes = GREATEST(storage_bytes - old_bytes, 0),
               updated_at    = now()
         WHERE workspace_id = wsid;
        RETURN OLD;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS e2a_messages_storage_delta_trg ON messages;
CREATE TRIGGER e2a_messages_storage_delta_trg
    AFTER INSERT OR UPDATE OR DELETE ON messages
    FOR EACH ROW EXECUTE FUNCTION e2a_messages_storage_delta();
