# Resource Schema Stability Audit

## Decision

Keep `/v1` stable by default and beta-mark only surfaces for which the repository
already records an explicit instability decision.

The audit covers all 130 generated component schemas, every operation root, and
field/value-level lifecycle annotations. It found one actionable gap:

1. `hold_reason` and the review-detail `protection` evidence are not yet stable,
   but their fields and component schemas have no machine-readable beta marker.

The unified review operations were described as beta in an earlier design, but
were released as stable in v1.0.10. They cannot be retroactively downgraded and
remain stable.

Templates, starter templates, protection configuration, versioned account-export
interiors, and experimental webhook event values are already marked correctly.
No other resource has an explicit instability decision, so all other schemas
remain stable.

## Contract changes

- Mark `MessageView.hold_reason`, `ReviewView.hold_reason`, and
  `MessageView.protection` as beta fields because their parent schemas are
  shared with stable operations.
- Mark `HoldReasonView` as a beta component.
- Mark `ProtectionFindingView` and `ThreatCategoryView` as beta components so
  compatibility tooling can evolve the technical evidence shape.
- Keep `MessageView`, `ReviewView`, `SendResultView`, and shared error types
  stable. The review-only `hold_reason` and `protection` fields remain beta
  without degrading their stable parent resources.

## Invariants

- A beta review operation must not degrade any schema shared with a stable
  operation.
- The canonical beta-operation inventory in code, generated OpenAPI, and
  `docs/api.md` must remain exactly equal.
- The one-time compatibility correction must match only the newly added review
  explanation and evidence paths as projected through their stable parents;
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
