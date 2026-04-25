-- Add outbound-only multi-recipient columns to messages table.
-- Inbound messages continue to use the singular recipient column.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS to_recipients text[];
ALTER TABLE messages ADD COLUMN IF NOT EXISTS cc text[];
ALTER TABLE messages ADD COLUMN IF NOT EXISTS bcc text[];

-- Backfill outbound messages only
UPDATE messages SET to_recipients = ARRAY[recipient]
  WHERE direction = 'outbound' AND recipient IS NOT NULL AND recipient != ''
    AND to_recipients IS NULL;
