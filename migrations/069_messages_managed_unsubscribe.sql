ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS managed_unsubscribe BOOLEAN NOT NULL DEFAULT false;
