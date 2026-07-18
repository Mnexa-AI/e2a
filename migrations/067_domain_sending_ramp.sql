-- Durable, active-day sending ramp for verified custom sender identities.
-- Existing verified domains are exempt so deployment cannot re-throttle an
-- established sender. New domains arm on their first eligible external send.

ALTER TABLE domains ADD COLUMN IF NOT EXISTS sending_ramp_status TEXT NOT NULL DEFAULT 'inactive';

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_sending_ramp_status_check
        CHECK (sending_ramp_status IN ('inactive', 'ramping', 'complete', 'exempt'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

UPDATE domains SET sending_ramp_status = 'exempt'
 WHERE sending_status = 'verified' AND sending_ramp_status = 'inactive';

-- Reputation and the daily pool are organizational-domain scoped, but tenant
-- isolated: sibling subdomains owned by one account share one progression;
-- unrelated accounts under a delegated suffix cannot throttle each other.
CREATE TABLE IF NOT EXISTS sending_ramp_scopes (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ramping' CHECK (status IN ('ramping', 'complete')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    active_days INTEGER NOT NULL DEFAULT 0 CHECK (active_days >= 0),
    last_qualified_day DATE,
    start_daily INTEGER NOT NULL CHECK (start_daily >= 50),
    target_daily INTEGER NOT NULL CHECK (target_daily >= start_daily),
    ramp_days INTEGER NOT NULL CHECK (ramp_days > 0),
    PRIMARY KEY (user_id, domain)
);

CREATE TABLE IF NOT EXISTS domain_send_counters (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    day DATE NOT NULL,
    reserved_count INTEGER NOT NULL DEFAULT 0 CHECK (reserved_count >= 0),
    confirmed_count INTEGER NOT NULL DEFAULT 0 CHECK (confirmed_count >= 0 AND confirmed_count <= reserved_count),
    daily_limit INTEGER NOT NULL CHECK (daily_limit > 0),
    PRIMARY KEY (user_id, domain, day)
);

CREATE TABLE IF NOT EXISTS sending_ramp_reservations (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    units INTEGER NOT NULL CHECK (units > 0),
    state TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved', 'confirmed', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id)
);

CREATE INDEX IF NOT EXISTS idx_domain_send_counters_day
    ON domain_send_counters (day);

CREATE INDEX IF NOT EXISTS idx_sending_ramp_reservations_state_updated
    ON sending_ramp_reservations (state, updated_at);
