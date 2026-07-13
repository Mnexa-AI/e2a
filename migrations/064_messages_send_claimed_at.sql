-- 064_messages_send_claimed_at.sql
--
-- Distinguish an active outbound provider call from River retry backoff. The
-- worker sets this lease immediately before provider I/O and clears it when a
-- side-effect-free failure is returned. A stale timestamp lets trash purges
-- recover from a worker crash or an exhausted terminal bookkeeping write.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS send_claimed_at TIMESTAMPTZ;
