# Inbound Email Authentication Contract

Date: 2026-07-20

## Problem statement

The current inbound API conflates three different concepts:

- the RFC 5322 `From` address;
- the address a reply should target; and
- the domain authentication evidence produced by SPF, DKIM, and DMARC.

`from` currently prefers `Reply-To`, while `authenticated_from` contains the
header From address even when authentication fails. `X-E2A-Auth-Verified` is
set when either SPF or DKIM passes without requiring alignment to the header
From domain. As a result, an unaligned SPF or DKIM pass can produce a signed
`Verified=true` claim for a spoofed From address. The REST message views also
surface an auth verdict without directly surfacing the identity that verdict
evaluated.

The public contract must expose literal email identities and standards-based
authentication evidence without claiming that SMTP proves a human or an exact
mailbox owner.

## Goals and non-goals

### Goals

- Make the RFC 5322 From address and SMTP envelope sender unambiguous.
- Make DMARC the developer-facing authentication decision.
- Expose SPF and every DKIM signature as supporting evidence, including
  identifier alignment.
- Use the DMARC verdict vocabulary defined for email authentication:
  `pass`, `fail`, `none`, `temperror`, and `permerror`, while retaining the
  additional standardized SPF and DKIM evidence statuses.
- Return one identical authentication object across REST, events, WebSocket,
  CLI, MCP, and both SDKs.
- Remove the redundant nested `X-E2A-Auth-*` attestation and `auth_headers`
  public field. Webhook envelope signatures remain the integrity mechanism for
  webhook delivery.
- Fail closed in server-side sender gates: only a DMARC pass can make a
  header-From allowlist/domain match trusted.
- Preserve honest, required response shapes for historical messages and
  non-SMTP messages without fabricating authentication evidence.

### Non-goals

- Proving the identity of a human or independent ownership of a mailbox local
  part. DMARC authenticates domain-authorized use of the header From domain.
- Adding ARC evaluation, BIMI, reputation scoring, spam scoring, or mailbox
  identity verification.
- Enforcing the sender domain's requested DMARC disposition at SMTP accept
  time. This change evaluates and surfaces the policy; e2a's existing inbound
  screening and review behavior remains responsible for disposition.
- Treating authenticated e2a loopback identity as SPF, DKIM, or DMARC evidence.

## Relevant context and constraints

- The Go handlers are the source of truth for the committed OpenAPI 3.1
  document. Any public model change must regenerate the OpenAPI document and
  both generated SDK bases.
- The unified message endpoints serve inbound, outbound, held, and loopback
  messages. A stable shape therefore requires a required-but-nullable
  `authentication` property: SMTP inbound carries an object; outbound and
  providerless loopback messages carry `null`.
- Current inbound rows persist an aggregate `{spf, dkim, dmarc}` JSON verdict,
  but do not retain all DKIM signatures, selectors, the complete envelope
  identity, or a fetched DMARC policy record.
- Current DMARC code computes relaxed identifier alignment but deliberately
  does not discover `_dmarc` policy records. The new five-state DMARC result
  requires full policy discovery and evaluation under RFC 9989 semantics.
- Webhook payloads already have an envelope signature. REST and WebSocket are
  authenticated transports. A second HMAC over nested auth headers does not
  create a useful additional trust boundary.
- Sender-supplied `X-E2A-Auth-*` MIME headers are untrusted input. They may
  remain inside canonical `raw_message` for forensic fidelity but must not be
  copied into parsed or trusted metadata.

## Proposed public design

### Canonical message shape

```json
{
  "id": "msg_123",
  "direction": "inbound",
  "header_from": "alice@example.com",
  "envelope_from": "bounce@mail.example.com",
  "reply_to": ["support@example.net"],
  "authentication": {
    "spf": {
      "status": "pass",
      "domain": "mail.example.com",
      "aligned": true,
      "detail": "SPF passed for mail.example.com"
    },
    "dkim": [
      {
        "status": "pass",
        "domain": "example.com",
        "selector": "s1",
        "aligned": true,
        "detail": "DKIM signature verified"
      }
    ],
    "dmarc": {
      "status": "pass",
      "domain": "example.com",
      "policy": "reject",
      "aligned_by": ["spf", "dkim"],
      "detail": "DMARC passed through aligned SPF and DKIM"
    }
  }
}
```

### Identity fields

- `header_from` replaces `from`. It is the parsed RFC 5322 From address and is
  never replaced by Reply-To. The property is required but nullable when the
  header is absent, malformed, or contains an unsupported multi-address form.
- `envelope_from` is the SMTP `MAIL FROM` address. It is nullable for a null
  reverse path and for messages that did not arrive over SMTP.
- `reply_to` remains an always-present array. Reply helpers choose its first
  address and fall back to `header_from`.
- `authenticated_from` is removed. The API does not present an email address as
  a separately verified human or mailbox identity.

### Authentication result vocabulary

The DMARC verdict is a closed response enum with exactly these values:

- `pass`: an applicable DMARC record exists and at least one authenticated
  identifier aligns.
- `fail`: an applicable record exists and no authenticated identifier aligns.
- `none`: no applicable DMARC policy record exists.
- `temperror`: a transient failure prevented a conclusive result.
- `permerror`: an unrecoverable evaluation error prevented a valid result.

SPF evidence additionally permits `neutral` and `softfail`. DKIM evidence
additionally permits `neutral` and `policy`, matching the registered
Authentication-Results vocabularies. These mechanism-specific statuses never
count as an aligned pass. All three status sets are closed response enums for
the GA contract; adding a new value requires an additive compatibility review.

### SPF result

```ts
interface SPFResult {
  status:
    | "pass" | "fail" | "none" | "neutral" | "softfail"
    | "temperror" | "permerror";
  domain: string | null;
  aligned: boolean | null;
  detail?: string;
}
```

`domain` is the authenticated RFC 5321 identity domain used by SPF. `aligned`
is `true` or `false` only when SPF passes; it is `null` for every other SPF
status. The evaluator must correctly identify whether SPF evaluated the
MAIL FROM or HELO identity, but that scope is not exposed in v1 unless needed
to represent null-reverse-path behavior correctly during implementation.

### DKIM results

```ts
interface DKIMResult {
  status:
    | "pass" | "fail" | "none" | "neutral" | "policy"
    | "temperror" | "permerror";
  domain: string | null;
  selector: string | null;
  aligned: boolean | null;
  detail?: string;
}
```

`dkim` is an always-present array because one message can carry multiple DKIM
signatures. Each signature is evaluated independently. `aligned` is non-null
only for a passing signature. An unsigned message carries `dkim: []`.

### DMARC result

```ts
interface DMARCResult {
  status: "pass" | "fail" | "none" | "temperror" | "permerror";
  domain: string | null;
  policy: "none" | "quarantine" | "reject" | null;
  aligned_by: Array<"spf" | "dkim">;
  detail?: string;
}
```

- `domain` is the RFC 5322 From domain evaluated by DMARC.
- `policy` is the effective requested policy from the applicable DMARC record.
  It is `null` when no applicable policy record exists or policy discovery
  cannot complete.
- `aligned_by` lists mechanisms that both passed and aligned. It is non-empty
  only for `status: pass` and contains each mechanism at most once.
- `policy` is not authentication strength and is not an authorization input. A
  message can pass under `policy: none` and fail under `policy: reject`.

There is no top-level `authentication.verdict`: it would duplicate
`authentication.dmarc.status` and create two fields that could disagree. The
single developer trust check is:

```ts
message.authentication?.dmarc.status === "pass"
```

### Requiredness

- The `authentication` JSON property is required on every message response.
- SMTP inbound messages carry a non-null authentication object whose `spf`,
  `dkim`, and `dmarc` properties are all required.
- Outbound and providerless loopback messages carry `authentication: null`.
  Loopback identity is application provenance, not email authentication.
- Every external inbound event carries a non-null authentication object.
  Loopback `email.received` events carry `authentication: null`.
- Arrays are never null. Nullable scalars are serialized explicitly as null so
  absence is not confused with incomplete server behavior.

### Removed public fields

The following are removed from OpenAPI, events, generated SDKs, CLI/MCP output,
and documentation:

- `from` (replaced by `header_from`);
- `authenticated_from`;
- `auth` (replaced by `authentication`);
- `auth_headers`; and
- all public `X-E2A-Auth-*` fields and verification helpers.

The webhook envelope signature remains unchanged and covers the entire event,
including `authentication`.

## Internal data and control flow

### Evaluation

For each SMTP inbound message, the relay performs these steps before policy
evaluation and persistence:

1. Parse and retain the RFC 5322 From address/domain and SMTP MAIL FROM
   address/domain as distinct values.
2. Evaluate SPF against the correct RFC 5321 identity.
3. Evaluate every DKIM signature, retaining domain, selector, result, and safe
   diagnostic detail. Existing refusal of DKIM `l=` signatures remains.
4. Discover the applicable DMARC record using the RFC 9989 DNS tree-walk
   algorithm and parse its effective policy and alignment modes.
5. Calculate strict or relaxed SPF and DKIM alignment according to the record.
6. Produce the canonical `EmailAuthentication` object once.
7. Use that same object for server policy, persistence, REST, and event
   publication. No consumer recomputes or reparses diagnostic strings.

DNS failures map to `temperror`; malformed or unusable policy records map to
`permerror`; no applicable policy record maps to `none`. Only `dmarc.status ==
pass` is authenticated.

### Server-side gating

- `inbound_policy=open` remains open and does not flag solely because
  authentication fails.
- `inbound_policy=allowlist` requires both `dmarc.status == pass` and an exact
  case-insensitive `header_from` match. A missing/non-pass verdict flags the
  message even when the claimed address is listed.
- `inbound_policy=domain` requires both `dmarc.status == pass` and a normalized
  authenticated From-domain match. A missing/non-pass verdict flags the
  message.
- Existing shared-relay resolvability checks remain an additional condition,
  not a substitute for authentication.
- Action-gate decisions consume `authentication.dmarc.status` rather than the
  legacy verdict structure. Missing and non-pass results remain fail-closed.

### Persistence

Add forward-only, idempotent columns sufficient to retain canonical evidence:

- `header_from`;
- `authentication JSONB`.

Reuse the existing nullable `messages.envelope_from` column (migration 055),
which currently stores outbound queue submissions, and populate it for new
SMTP inbound rows as well. Direction disambiguates the same wire concept:
outbound stores the submitted MAIL FROM; inbound stores the received MAIL FROM.

New inbound writes populate all three atomically with the message and event
outbox entry. Outbound and loopback writes store `authentication = NULL`.

Historical inbound rows cannot be upgraded to a standards-complete DMARC
result because the connecting context, all DKIM signatures, and policy lookup
at receipt time were not retained. Backfill them fail-closed:

- parse `header_from` from `raw_message` when possible;
- leave `envelope_from` null unless a trustworthy stored value exists; and
- return an authentication object with `dmarc.status = permerror`, null policy,
  empty `aligned_by`, and a diagnostic explaining that complete evidence
  predates the evaluator. Existing SPF/DKIM statuses may be retained as
  diagnostics, but alignment is null unless it can be proven from persisted
  machine-readable evidence.

Do not translate a legacy `dmarc=pass` into a new standards-complete pass when
the applicable policy record was never discovered.

Once all read/write paths use the new columns, retain the old columns only for
the minimum compatibility window required by migration safety; remove them in
a later forward migration rather than destructively altering the messages
table in place.

## API and client-surface changes

The change is intentionally breaking and must remain synchronized across all
eight client surfaces:

1. Go identity model, relay evaluator, persistence, and Huma response views.
2. OpenAPI schema and examples.
3. TypeScript generated base and high-level client.
4. Python generated base, sync client, and async client.
5. CLI message/listen output and fixtures.
6. MCP message tools and schemas.
7. Web dashboard message models and authentication presentation.
8. Webhook and WebSocket event payload types and examples.

Documentation and the agent plugin must stop instructing consumers to authorize
on `authenticated_from`. Examples must use the DMARC status plus the literal
`header_from` only when an address-level application policy also matters:

```ts
const trusted =
  message.authentication?.dmarc.status === "pass" &&
  message.header_from?.toLowerCase() === configuredAddress.toLowerCase();
```

This means “the configured From address was authorized by an authenticated
domain,” not “e2a proved the human behind the mailbox.”

## Edge cases and failure handling

- Missing, malformed, or multiple incompatible From identities fail closed and
  cannot produce DMARC pass.
- Null reverse path uses the appropriate SPF identity if supported; otherwise
  SPF is `none` without affecting a valid aligned DKIM pass.
- Multiple DKIM signatures are all retained. One aligned passing signature is
  sufficient for DMARC pass even when other signatures fail or are unaligned.
- A passing SPF/DKIM identity that is unaligned remains visible as evidence but
  cannot produce DMARC pass.
- Public-suffix and organizational-domain discovery follows RFC 9989 rather
  than the current eTLD+1-only approximation.
- DNS timeouts and temporary resolver failures produce `temperror`; they do not
  silently degrade to `fail` or `none`.
- Malformed DMARC records produce `permerror`.
- Sender-supplied `X-E2A-Auth-*` headers remain only in raw MIME and never
  override or merge into the server-owned object.
- Diagnostic `detail` strings are non-contractual, safe for display, and never
  parsed by server or client logic.
- Webhook retries and WebSocket reconnect drains reuse the exact persisted
  event envelope, preserving authentication object equality across channels.

## Scalability and extensibility

- DNS lookups must honor bounded timeouts and the existing SMTP/River retry
  model. Cache positive and negative DMARC policy discoveries for a bounded,
  conservative interval with bounded memory; do not cache transient resolver
  errors.
- Store structured evidence once so list/read/event paths do not perform DNS or
  MIME authentication work.
- The DKIM array permits multiple signatures without a future schema break.
- ARC can later be added as a sibling evidence field without changing DMARC
  semantics. It must not silently turn `dmarc.status` into pass.
- Additional policy metadata can be added to the DMARC object without changing
  the developer trust check.

## Rollout

1. Add idempotent `header_from` and `authentication` persistence fields, reuse
   the existing `envelope_from` field for inbound, and add dual-read support.
2. Implement the RFC 9989 evaluator and canonical internal model behind unit
   tests.
3. Switch new inbound writes and server-side gates to the canonical model.
4. Switch REST and event response models in one breaking API change.
5. Regenerate OpenAPI and SDK bases; update all hand-written clients and
   consumer surfaces.
6. Update docs, examples, plugins, and security guidance.
7. Run spec drift, SDK freshness, compatibility-policy, and full surface tests.
8. Remove dead `X-E2A-Auth-*` signing and verification code only after a
   repository-wide reference check proves no internal consumer remains.

Because the field semantics are intentionally corrected rather than aliased,
there is no period in which both `from` and `header_from`, or both `auth` and
`authentication`, are advertised as canonical.

## Verification strategy

### Email authentication unit tests

- SPF pass aligned and unaligned under strict and relaxed modes.
- Every SPF terminal/error status.
- Multiple DKIM signatures with mixed pass/fail/alignment results.
- DKIM `l=` refusal remains enforced.
- DMARC record discovery at the exact From domain and through RFC 9989 tree
  walk.
- `p`, `sp`, `aspf`, and `adkim` behavior.
- No record, malformed record, DNS timeout, and resolver failure mappings.
- Public-suffix and organizational-domain boundaries.

### Relay and policy regression tests

- Attacker-controlled MAIL FROM passes SPF while spoofed header From fails
  DMARC and never becomes trusted.
- Unaligned passing DKIM never authenticates header From.
- Allowlisted spoofed From is flagged when DMARC is not pass.
- Domain-gated spoof is flagged even when SPF authenticates the attacker's
  envelope domain.
- Valid aligned SPF-only, DKIM-only, and dual-aligned messages pass.
- Open policy continues to deliver non-passing mail with evidence intact.

### Persistence and event tests

- Canonical authentication object round-trips without loss.
- REST detail, list summary, webhook, and WebSocket representations are
  structurally equal.
- Historical rows return fail-closed `permerror` authentication objects.
- Outbound and loopback messages serialize required `authentication: null`.
- Sender-injected auth headers cannot affect structured results.

### Contract and client tests

- OpenAPI requires the new fields and removes all retired fields.
- TS and Python generated types model required/nullability and closed enums.
- CLI, MCP, web, and plugin fixtures use `header_from` and DMARC status.
- Existing webhook signature verification still validates the whole revised
  payload.
- `make spec-check`, generated-code freshness checks, SDK tests, CLI/MCP/web
  tests, Go unit/integration tests, and repository text-integrity checks pass.

## Open questions

No product or contract questions remain. For a null reverse path, the SPF
result's `domain` contains the evaluated HELO domain while `envelope_from`
remains null; no additional public scope field is required for the approved
developer trust decision.
