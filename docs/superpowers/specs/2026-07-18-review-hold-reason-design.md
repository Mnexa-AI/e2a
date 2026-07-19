# Review Hold Reason Product and API Design

## Problem statement

Reviewers can see that a message is pending, but the new PR stack exposes the explanation through three implementation-oriented fields: `review_reason`, `scan_score`, and `protection[]`. Every client must infer which producer actually caused the hold, translate internal codes, and avoid attaching secondary scan evidence to a gate decision. This produces inconsistent copy and makes it too easy to misstate why a message is waiting.

The desired outcome is one authoritative, plain-language explanation that works for both policy-gate and content-scan holds. It must be immediately visible in the web queue and safely enriched in the detail response.

## Goals

- Return a single `hold_reason` object on review list and detail responses.
- Explain gate and scan holds with equal clarity.
- Show a plain summary in every collapsed review row.
- Put detector category, rationale, and confidence behind expansion.
- Preserve account scoping and prevent review data from leaking through agent message APIs.
- Keep technical `protection[]` evidence available on detail responses as a beta surface.

## Non-goals

- No database migration or new denormalized columns.
- No localization framework in this slice.
- No change to screening decisions, thresholds, approval behavior, or audit persistence.
- No attempt to expose raw detector payloads or flagged spans.

## API contract

Both `ReviewView` and the review-only `MessageView` return an optional `hold_reason`:

```json
{
  "hold_reason": {
    "type": "scan",
    "code": "inbound_scan",
    "summary": "Content screening found a potential risk.",
    "category": "prompt_injection_direct",
    "detail": "It asks the agent to ignore its instructions and wire funds.",
    "confidence": 0.92
  }
}
```

`type`, `code`, and `summary` are present whenever `messages.review_reason` is populated. `category`, `detail`, and `confidence` are optional detail enrichment.

Gate example:

```json
{
  "hold_reason": {
    "type": "gate",
    "code": "sender_gate",
    "summary": "This sender isn't allowed by the inbox policy."
  }
}
```

Known summary mappings:

| Code | Type | Summary |
|---|---|---|
| `sender_gate` | `gate` | This sender isn't allowed by the inbox policy. |
| `recipient_gate` | `gate` | One or more recipients aren't allowed by the inbox policy. |
| `inbound_scan` | `scan` | Content screening found a potential risk. |
| `outbound_scan` | `scan` | Content screening found a potential risk. |
| `outbound_send` | `send` | This outbound message requires review before sending. |

Unknown non-empty codes return `type: "unknown"`, retain the original `code`, and use `summary: "This message requires review."` Empty codes omit `hold_reason` for compatibility with old rows.

Because these PR fields have not shipped, `hold_reason` replaces the proposed public `review_reason` and `scan_score` fields rather than duplicating them. The internal message columns remain unchanged.

## Data flow

`GET /v1/reviews` builds the base object entirely from `messages.review_reason`. This keeps the paginated list query cheap and avoids an audit-table join. A scan summary is intentionally generic in the list.

`GET /v1/reviews/{id}` first builds the same base object, then best-effort enriches it from `protection_events` only when the driving code is `inbound_scan` or `outbound_scan`. The first scan finding supplies its highest-confidence category, a validated flagged-detector rationale, and category confidence with aggregate score as fallback.

`protection[]` continues to expose curated technical evidence on detail. Secondary gate or scan events may appear there, but they never replace the primary `hold_reason`.

## Rationale safety

A rationale is eligible only when its per-detector result has `status: "ok"`, `flagged: true`, and a non-empty `provider.native_verdict`. Failed, malformed, unsupported, timed-out, and unflagged results never provide `detail`. The full raw provider payload is never serialized.

If protection-event lookup, category decoding, or rationale validation fails, the detail response retains the base summary and omits enrichment.

## Web UX

The collapsed row always shows the API-provided summary beneath sender/recipient metadata:

```text
Urgent transfer request
finance@example.com → payments@acme.ai
⚑ Content screening found a potential risk
```

The expanded panel shows a calm callout headed `Why this message was held`. For enriched scans it shows the friendly category label and rationale. Numeric confidence appears only inside a `Screening details` disclosure, never in the collapsed row and never on gate holds.

The UI does not reconstruct meaning from `code`, `protection[]`, or score. It uses `hold_reason.summary` as the source of truth and treats enrichment as optional.

## Edge cases

- Gate review plus secondary scan flag: gate summary remains primary; scan evidence may remain technical detail only.
- Equal-severity gate and scan where screening attribution chooses scan: scan summary is primary.
- Unknown code: generic review summary, no crash or humanized internal token.
- Missing reason on a legacy row: omit the line instead of inventing a cause.
- Malformed category JSON: omit category while retaining the base summary.
- Non-finite or out-of-range confidence: omit it from the public object.
- Detail lookup failure: log server-side and degrade to the base object.

## Verification

- Unit-test every known and unknown code mapping.
- Test list and detail serialization for gate and scan holds.
- Test mixed gate-review/scan-flag attribution.
- Test successful flagged rationale enrichment and reject error/unflagged/malformed raw results.
- Test that agent-facing message serialization omits both `hold_reason` and `protection`.
- Test collapsed and expanded UI variants, including confidence placement and absent enrichment.
- Regenerate OpenAPI, TypeScript SDK, and Python SDK artifacts and run freshness/contract gates.

