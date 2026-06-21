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
UPDATE agent_identities
   SET outbound_policy        = 'allowlist',
       outbound_allowlist     = '{}',
       outbound_policy_action = 'review'
 WHERE hitl_enabled = true
   AND COALESCE(hitl_mode, 'all') = 'all'
   AND outbound_policy = 'open';

-- hitl_mode='high_impact' while enabled ⇒ approximated by enabling the outbound
-- content scan at its default thresholds (migration 041). This is an approximation
-- of the old "high-impact action on untrusted inbound" heuristic — call out in the
-- release notes.
UPDATE agent_identities
   SET outbound_scan = 'on'
 WHERE hitl_enabled = true
   AND hitl_mode = 'high_impact'
   AND outbound_scan = 'off';
