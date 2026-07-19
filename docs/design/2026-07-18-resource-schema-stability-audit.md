# Resource Schema Stability Audit

## Decision

Keep `/v1` stable by default and beta-mark only surfaces for which the repository
already records an explicit instability decision.

The audit covers all 130 generated component schemas, every operation root, and
field/value-level lifecycle annotations. It found one actionable gap:

1. `MessageView.protection`, `ProtectionFindingView`, and
   `ThreatCategoryView` are documented as beta review-detail evidence, but the
   generated contract has no machine-readable beta marker for them.

The unified review operations were described as beta in an earlier design, but
were released as stable in v1.0.10. They cannot be retroactively downgraded and
remain stable.

Templates, starter templates, protection configuration, versioned account-export
interiors, and experimental webhook event values are already marked correctly.
No other resource has an explicit instability decision, so all other schemas
remain stable.

## Contract changes

- Mark `MessageView.protection` as a beta field because `MessageView` is shared
  with stable message endpoints.
- Mark `ProtectionFindingView` and `ThreatCategoryView` as beta components so
  compatibility tooling can evolve the technical evidence shape.
- Keep `MessageView`, `SendResultView`, shared error types, and `HoldReasonView`
  stable. `hold_reason` is the product-facing explanation and is intentionally
  the durable API layer; `protection` is optional technical evidence.

## Invariants

- A beta review operation must not degrade any schema shared with a stable
  operation.
- The canonical beta-operation inventory in code, generated OpenAPI, and
  `docs/api.md` must remain exactly equal.
- The one-time compatibility correction must match only the three newly added
  evidence paths as projected through the endpoints that share `MessageView`;
  every other stable-to-beta downgrade remains an error.
- A prose-only beta label is insufficient: each unstable operation, field, or
  component must carry the canonical extension.
- Runtime JSON, persistence, authorization, and review behavior do not change.

## Verification

- Contract tests assert the exact beta operation set.
- Contract tests assert the three review-evidence markers and stable shared
  anchors.
- Regenerate OpenAPI and both SDKs, then run focused stability tests and the
  repository's generated-artifact freshness checks.
