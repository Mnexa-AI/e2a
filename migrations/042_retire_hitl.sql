-- 042_retire_hitl.sql
--
-- Slice 5b retires hitl_enabled / hitl_mode as PRODUCER policy. The outbound
-- recipient gate (outbound_policy) + content scan (outbound_scan) now own holds;
-- the HITL mechanism fields (hitl_ttl_seconds, hitl_expiration_action) survive as
-- the review-queue knobs. This migration maps the old behavior forward onto the
-- outbound policy so existing agents keep holding what they held before.
--
-- The columns hitl_enabled / hitl_mode are NOT dropped here — two-step retirement
-- (a later migration drops them once no deployed binary reads them) keeps rollback
-- safe per the design. Idempotent + non-destructive: only rewrites agents still at
-- the outbound defaults, so a re-run (or an already-configured agent) is untouched.

-- hitl_mode='all' (the default) while enabled ⇒ hold every outbound send. Mapped to
-- an allowlist gate with an empty list + review action — the trust-ramp (grow the
-- allowlist) replaces hitl_mode=all.
--
-- Wrapped in a column-existence guard so this is idempotent and safe to re-run
-- after 043 has dropped hitl_enabled/hitl_mode (a no-op then) — the migration must
-- not reference columns a later migration removed.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_name = 'agent_identities' AND column_name = 'hitl_enabled'
  ) THEN
    UPDATE agent_identities
       SET outbound_policy        = 'allowlist',
           outbound_allowlist     = '{}',
           outbound_policy_action = 'review'
     WHERE hitl_enabled = true
       AND COALESCE(hitl_mode, 'all') = 'all'
       AND outbound_policy = 'open';

    -- hitl_mode='high_impact' held high-impact RECIPIENTS — a reply/forward reaching
    -- a party off the agent's own domain. That is fundamentally a recipient gate, so
    -- preserve it as one: outbound_policy='domain' + review holds any off-domain
    -- recipient for a human. (A content scan alone would NOT replace it — it never
    -- inspects recipients, so a benign forward to a new third party would slip
    -- through; adversarial review confirmed that regression.) Also enable the scan.
    UPDATE agent_identities
       SET outbound_policy        = 'domain',
           outbound_policy_action = 'review',
           outbound_scan          = 'on'
     WHERE hitl_enabled = true
       AND hitl_mode = 'high_impact'
       AND outbound_policy = 'open';
  END IF;
END $$;
