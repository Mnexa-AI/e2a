# Inbound Email Authentication Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace ambiguous inbound sender/auth fields with literal header and envelope identities plus standards-based SPF, multi-signature DKIM, and RFC 9989 DMARC results on every API and event surface.

**Architecture:** Build one canonical `emailauth.Authentication` value during SMTP intake, persist it atomically with `header_from` and the existing `envelope_from` column, and reuse it unchanged for policy enforcement, REST, webhook, and WebSocket delivery. Remove nested `X-E2A-Auth-*` attestations; webhook envelope signatures and authenticated REST/WS transports remain the delivery-integrity boundary.

**Tech Stack:** Go 1.26, PostgreSQL/pgx, Huma/OpenAPI 3.1, `go-msgauth/dkim`, `blitiri.com.ar/go/spf`, npm workspaces/TypeScript/Vitest, Python 3.9+/pytest/mypy, Next.js/Jest.

---

## File map

- Create `internal/emailauth/result.go` — canonical wire-safe authentication types and closed status vocabularies.
- Create `internal/emailauth/dmarc.go` — DMARC record discovery, parsing, strict/relaxed alignment, and five-state verdict derivation.
- Create `internal/emailauth/dmarc_test.go` — deterministic resolver-driven RFC 9989 tests.
- Modify `internal/emailauth/checker.go` and `checker_test.go` — context-aware canonical evaluator, full SPF evidence, every DKIM signature, and removal of `DomainAuthenticated`.
- Create `migrations/071_inbound_authentication.sql` — additive `header_from` and `authentication` columns; reuse migration 055's `envelope_from`.
- Modify `internal/identity/store.go`, `review.go`, `user_data_rights.go`, and DB tests — write/read the canonical evidence on every message projection.
- Modify `internal/inboundpolicy/policy.go` and tests — gated policies require DMARC pass in addition to the address/domain match.
- Modify `internal/piguard/piguard.go` and tests — consume the canonical authentication object without changing screening behavior.
- Modify `internal/relay/server.go`, `screening.go`, and relay tests — evaluate once, persist identities/evidence, build canonical events, and stop signing nested auth headers.
- Modify `internal/eventpayload/payloads.go`, fixtures, and tests — `header_from`, `envelope_from`, and required nullable `authentication` on `email.received`.
- Modify `internal/ws/handler.go` and tests plus `internal/agent/selfsend.go` and tests — exact reconnect parity and `authentication: null` for loopback.
- Modify `internal/httpapi/messages.go`, schema tests, operation tests, and export stability tests — canonical required/null API fields and retired schema removal.
- Remove `internal/headers/signer.go` and its tests after all references are removed; simplify relay/test constructors that currently accept a signer.
- Regenerate `api/openapi.yaml`, TypeScript generated models, and Python generated models.
- Modify hand-written TS/Python webhook payload types and tests.
- Modify CLI listen forwarding/tests, MCP message projection/tests, dashboard types/projections/rendering/tests, docs, examples, and plugin/agentify fixtures.

## Task 1: Canonical authentication types

**Files:**
- Create: `internal/emailauth/result.go`
- Modify: `internal/emailauth/checker_test.go`
- Modify: `internal/emailauth/checker.go`

- [ ] **Step 1: Write compile-time and JSON-shape tests for the approved model**

Add tests that construct and marshal the canonical shape:

```go
func TestAuthenticationJSONShape(t *testing.T) {
	spfDomain, dkimDomain, selector, policy := "mail.example.com", "example.com", "s1", DMARCPolicyReject
	spfAligned, dkimAligned := true, true
	a := Authentication{
		SPF: SPFResult{Status: StatusPass, Domain: &spfDomain, Aligned: &spfAligned},
		DKIM: []DKIMResult{{Status: StatusPass, Domain: &dkimDomain, Selector: &selector, Aligned: &dkimAligned}},
		DMARC: DMARCResult{Status: StatusPass, Domain: &dkimDomain, Policy: &policy, AlignedBy: []AlignmentMechanism{AlignedBySPF, AlignedByDKIM}},
	}
	b, err := json.Marshal(a)
	if err != nil { t.Fatal(err) }
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil { t.Fatal(err) }
	if got["spf"] == nil || got["dkim"] == nil || got["dmarc"] == nil { t.Fatalf("shape = %s", b) }
}

func TestNonPassAlignmentIsNull(t *testing.T) {
	r := SPFResult{Status: StatusFail}
	if r.Aligned != nil { t.Fatalf("aligned = %v, want nil", *r.Aligned) }
}
```

- [ ] **Step 2: Run the test and verify it fails to compile**

Run:

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -run 'TestAuthenticationJSONShape|TestNonPassAlignmentIsNull'
```

Expected: FAIL because `Authentication`, `SPFResult`, `DKIMResult`, and `DMARCResult` do not exist.

- [ ] **Step 3: Add the canonical types**

Create `result.go` with these exact public fields:

```go
package emailauth

type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusNone Status = "none"
	StatusNeutral Status = "neutral"
	StatusSoftFail Status = "softfail"
	StatusPolicy Status = "policy"
	StatusTempError Status = "temperror"
	StatusPermError Status = "permerror"
)

type AlignmentMechanism string
const (
	AlignedBySPF AlignmentMechanism = "spf"
	AlignedByDKIM AlignmentMechanism = "dkim"
)

type DMARCPolicy string
const (
	DMARCPolicyNone DMARCPolicy = "none"
	DMARCPolicyQuarantine DMARCPolicy = "quarantine"
	DMARCPolicyReject DMARCPolicy = "reject"
)

type SPFResult struct {
	Status Status `json:"status" enum:"pass,fail,none,neutral,softfail,policy,temperror,permerror"`
	Domain *string `json:"domain" nullable:"true"`
	Aligned *bool `json:"aligned" nullable:"true"`
	Detail string `json:"detail,omitempty"`
}

type DKIMResult struct {
	Status Status `json:"status" enum:"pass,fail,none,neutral,policy,temperror,permerror"`
	Domain *string `json:"domain" nullable:"true"`
	Selector *string `json:"selector" nullable:"true"`
	Aligned *bool `json:"aligned" nullable:"true"`
	Detail string `json:"detail,omitempty"`
}

type DMARCResult struct {
	Status Status `json:"status" enum:"pass,fail,none,temperror,permerror"`
	Domain *string `json:"domain" nullable:"true"`
	Policy *DMARCPolicy `json:"policy" nullable:"true"`
	AlignedBy []AlignmentMechanism `json:"aligned_by" nullable:"false"`
	Detail string `json:"detail,omitempty"`
}

type Authentication struct {
	SPF SPFResult `json:"spf"`
	DKIM []DKIMResult `json:"dkim" nullable:"false"`
	DMARC DMARCResult `json:"dmarc"`
}

func (a *Authentication) Passed() bool {
	return a != nil && a.DMARC.Status == StatusPass
}
```

Delete the old `CheckStatus`, `CheckResult`, `AuthVerdict`, `DomainAuthenticated`, and `Summary` definitions from `checker.go` only after their call sites are migrated in later tasks.

- [ ] **Step 4: Run the focused tests**

Run:

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -run 'TestAuthenticationJSONShape|TestNonPassAlignmentIsNull'
```

Expected: PASS.

- [ ] **Step 5: Commit the canonical model**

```bash
git add internal/emailauth/result.go internal/emailauth/checker.go internal/emailauth/checker_test.go
git commit -m "feat(emailauth): define canonical authentication model"
```

## Task 2: RFC 9989 DMARC discovery and alignment

**Files:**
- Create: `internal/emailauth/dmarc.go`
- Create: `internal/emailauth/dmarc_test.go`

- [ ] **Step 1: Write resolver-driven DMARC tests**

Define a fake resolver and table tests covering exact-domain lookup, organizational tree walk, no record, malformed record, temporary DNS failure, strict alignment, relaxed alignment, `p`, `sp`, `aspf`, and `adkim`:

```go
type fakeTXTResolver struct {
	records map[string][]string
	errors map[string]error
}
func (f fakeTXTResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if err := f.errors[name]; err != nil { return nil, err }
	return f.records[name], nil
}

func TestEvaluateDMARCAlignedDKIM(t *testing.T) {
	r := fakeTXTResolver{records: map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject"}}}
	dkimDomain, selector, aligned := "example.com", "s1", true
	got := evaluateDMARC(context.Background(), r, "example.com",
		SPFResult{Status: StatusNone},
		[]DKIMResult{{Status: StatusPass, Domain: &dkimDomain, Selector: &selector, Aligned: &aligned}},
	)
	if got.Status != StatusPass || len(got.AlignedBy) != 1 || got.AlignedBy[0] != AlignedByDKIM { t.Fatalf("got %+v", got) }
	if got.Policy == nil || *got.Policy != DMARCPolicyReject { t.Fatalf("policy = %v", got.Policy) }
}
```

Use a sentinel temporary error implementing `Temporary() bool` and assert it maps to `temperror`; malformed duplicate `p=` tags map to `permerror`; NXDOMAIN/no TXT maps to `none`.

- [ ] **Step 2: Run the DMARC tests and verify they fail**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -run TestEvaluateDMARC -count=1
```

Expected: FAIL because `evaluateDMARC` and the resolver interface do not exist.

- [ ] **Step 3: Implement bounded DMARC lookup and record parsing**

In `dmarc.go`, define:

```go
type TXTResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type netTXTResolver struct{ resolver *net.Resolver }
func (r netTXTResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return r.resolver.LookupTXT(ctx, name)
}

type dmarcRecord struct {
	Policy DMARCPolicy
	SubdomainPolicy *DMARCPolicy
	SPFStrict bool
	DKIMStrict bool
}
```

Implement `discoverDMARCRecord`, `parseDMARCRecord`, and `evaluateDMARC`. Query `_dmarc.<header-from-domain>` first, then perform the RFC 9989 DNS tree walk without crossing the public-suffix boundary. Accept exactly one `v=DMARC1` record; reject malformed/duplicate required tags as `permerror`. Apply `sp` when the discovered policy belongs to an organizational parent. Use exact comparison for strict alignment and organizational-domain comparison for relaxed alignment.

Wrap each lookup in a bounded child context:

```go
lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()
```

Do not cache `temperror`. Add a small mutex-protected cache capped at 1,024 entries with a conservative five-minute expiry for positive and no-record results; inject the resolver so tests never perform network I/O.

- [ ] **Step 4: Run all DMARC tests**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -run 'TestEvaluateDMARC|TestDiscoverDMARC|TestParseDMARC' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit DMARC evaluation**

```bash
git add internal/emailauth/dmarc.go internal/emailauth/dmarc_test.go
git commit -m "feat(emailauth): evaluate RFC 9989 DMARC policy"
```

## Task 3: SPF and multi-signature DKIM evidence

**Files:**
- Modify: `internal/emailauth/checker.go`
- Modify: `internal/emailauth/checker_test.go`

- [ ] **Step 1: Add failing evaluator tests**

Add tests proving: SPF softfail remains `softfail`; every DKIM signature is returned; an `l=` signature is refused; an attacker envelope can pass SPF while the spoofed header From yields DMARC fail; and an aligned DKIM pass yields DMARC pass.

```go
func TestAuthenticationPassRequiresDMARCAlignment(t *testing.T) {
	spfDomain, fromDomain := "evil.com", "trusted.com"
	aligned := false
	spf := SPFResult{Status: StatusPass, Domain: &spfDomain, Aligned: &aligned}
	dmarc := evaluateDMARC(context.Background(), fakeTXTResolver{records: map[string][]string{
		"_dmarc.trusted.com": {"v=DMARC1; p=reject"},
	}}, fromDomain, spf, nil)
	if dmarc.Status != StatusFail { t.Fatalf("DMARC = %+v", dmarc) }
}
```

- [ ] **Step 2: Run the emailauth package and verify the old aggregate implementation fails**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -count=1
```

Expected: FAIL because `checkSPF` and `checkDKIM` return legacy aggregate types and DMARC does not discover policy.

- [ ] **Step 3: Replace the aggregate evaluator**

Change the public entry point to:

```go
func Check(ctx context.Context, remoteIP net.IP, envelopeFrom string, rawMessage []byte) *Authentication {
	return checkWithResolver(ctx, netTXTResolver{resolver: net.DefaultResolver}, remoteIP, envelopeFrom, rawMessage)
}
```

Parse `headerFromDomain` once. Make `checkSPF` return `SPFResult`, retaining the evaluated envelope domain and preserving `softfail` and `neutral` instead of folding them into fail. Make `checkDKIM` return `[]DKIMResult`, one result per verification with domain and selector; an unsigned message returns `[]`. Because `go-msgauth/dkim.Verification` does not expose the selector, parse ordered DKIM-Signature tag maps from the raw header block and correlate each `s=`/`l=` value with the verification at the same index. Refuse only signatures carrying `l=` without discarding safe results from other signatures. Populate mechanism `Aligned` pointers only for passing results, then call `evaluateDMARC`.

- [ ] **Step 4: Run emailauth tests**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/emailauth -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the evaluator**

```bash
git add internal/emailauth/checker.go internal/emailauth/checker_test.go
git commit -m "feat(emailauth): retain SPF and DKIM evidence"
```

## Task 4: Persistence migration and canonical round-trip

**Files:**
- Create: `migrations/071_inbound_authentication.sql`
- Modify: `internal/identity/store.go`
- Modify: `internal/identity/review.go`
- Modify: `internal/identity/user_data_rights.go`
- Modify: `internal/identity/store_test.go`

- [ ] **Step 1: Write the DB-backed round-trip test**

Replace `TestInboundMessageRoundTripsAuthVerdict` with a test that creates an inbound message carrying distinct header/envelope identities and the complete authentication object, then reads it through `GetInboundMessage`, list, detail, review, and export paths.

```go
authn := &emailauth.Authentication{
	SPF: emailauth.SPFResult{Status: emailauth.StatusPass, Domain: ptr("mail.example.com"), Aligned: boolPtr(true)},
	DKIM: []emailauth.DKIMResult{},
	DMARC: emailauth.DMARCResult{Status: emailauth.StatusPass, Domain: ptr("example.com"), Policy: policyPtr(emailauth.DMARCPolicyReject), AlignedBy: []emailauth.AlignmentMechanism{emailauth.AlignedBySPF}},
}
```

Assert `HeaderFrom == "alice@example.com"`, `EnvelopeFrom == "bounce@mail.example.com"`, and deep equality of `Authentication`.

- [ ] **Step 2: Run the DB test and verify it fails**

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable' GOCACHE=/tmp/e2a-go-build-cache go test -tags integration -p 1 ./internal/identity -run TestInboundMessageRoundTripsAuthentication -count=1
```

Expected: FAIL because the migration and store fields do not exist.

- [ ] **Step 3: Add the idempotent migration**

Create:

```sql
-- 071_inbound_authentication.sql
ALTER TABLE messages ADD COLUMN IF NOT EXISTS header_from TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS authentication JSONB;
```

Do not add `envelope_from`; migration 055 already created it.

- [ ] **Step 4: Replace identity storage fields and SQL projections**

In `identity.Message`, replace `Sender`, `AuthHeaders`, and `Auth` as public authentication sources with:

```go
HeaderFrom string `json:"header_from"`
EnvelopeFrom string `json:"envelope_from"`
Authentication *emailauth.Authentication `json:"authentication"`
```

Keep any internal outbound sender field needed for composition under an unexported/internal name until Task 6 changes the API mapping. Change `CreateInboundMessage`/`CreateInboundMessageInTx` to accept `headerFrom`, `envelopeFrom`, and `*emailauth.Authentication`; marshal the object inside the store and write `sender`, `header_from`, `envelope_from`, and `authentication` atomically. Update every SELECT/Scan in `store.go`, `review.go`, and `user_data_rights.go` to return the same fields. Add `unmarshalAuthentication` that treats SQL NULL as nil and rejects malformed non-null JSON.

For legacy inbound rows with `authentication IS NULL` and `auth_verdict IS NOT NULL`, return a fail-closed canonical object whose DMARC status is `permerror`, policy is null, and detail is `"authentication evidence predates RFC 9989 evaluation"`; never promote legacy pass.

- [ ] **Step 5: Run identity tests serially**

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable' GOCACHE=/tmp/e2a-go-build-cache go test -tags integration -p 1 ./internal/identity -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit persistence**

```bash
git add migrations/071_inbound_authentication.sql internal/identity/store.go internal/identity/review.go internal/identity/user_data_rights.go internal/identity/store_test.go
git commit -m "feat(identity): persist inbound authentication evidence"
```

## Task 5: Relay enforcement and canonical received events

**Files:**
- Modify: `internal/inboundpolicy/policy.go`
- Modify: `internal/inboundpolicy/policy_test.go`
- Modify: `internal/relay/server.go`
- Modify: `internal/relay/screening.go`
- Modify: `internal/piguard/piguard.go`
- Modify: `internal/piguard/piguard_test.go`
- Modify: `internal/relay/inbound_process_test.go`
- Modify: `internal/relay/webhook_payload_test.go`
- Modify: `internal/e2e/inbound_policy_e2e_test.go`
- Modify: `internal/eventpayload/payloads.go`
- Modify: `internal/eventpayload/golden_test.go`
- Modify: `internal/eventpayload/testdata/email.received.json`
- Modify: `internal/eventpayload/testdata/email.received.min.json`

- [ ] **Step 1: Add spoof-regression tests at the policy boundary**

Change the evaluator signature to accept a DMARC status and assert gated policies fail closed:

```go
d := EvaluateIngestion(Allowlist, []string{"approver@trusted.com"}, "approver@trusted.com", true, "fail")
if !d.Flagged { t.Fatal("spoofed allowlist match must be flagged") }
d = EvaluateIngestion(Domain, []string{"trusted.com"}, "approver@trusted.com", true, "pass")
if d.Flagged { t.Fatalf("authenticated domain match flagged: %s", d.Reason) }
```

Add a relay regression with attacker MAIL FROM `attacker@evil.com`, header From `approver@trusted.com`, SPF pass evidence, and DMARC fail. Assert the persisted message is flagged, the event's `header_from` is literal, and no retired auth fields exist.

- [ ] **Step 2: Run focused relay/policy tests and verify failure**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/inboundpolicy ./internal/relay -run 'TestEvaluateIngestion|TestInboundSpoofedFrom' -count=1
```

Expected: FAIL because the gate ignores DMARC and events use retired fields.

- [ ] **Step 3: Require DMARC pass for gated policies**

Change the policy API to:

```go
func EvaluateIngestion(policy string, allowlist []string, headerFrom string, senderResolvable bool, dmarcStatus emailauth.Status) Decision
```

To keep `inboundpolicy` a stdlib leaf, pass the status as a string instead of importing `emailauth` if the existing package-boundary rule is retained. For `allowlist` and `domain`, return a flagged decision before matching when status is not `pass`; `open` remains unchanged.

Replace `piguard.Request.Auth *emailauth.AuthVerdict` with
`*emailauth.Authentication`. Screening detectors continue to receive evidence
for context, but no detector may reinterpret a non-pass DMARC result as trusted.

- [ ] **Step 4: Replace relay signing with canonical evaluation**

Call `emailauth.Check(ctx, in.RemoteIP, extractEmail(in.EnvelopeFrom), in.Body)`. Remove `DomainAuthenticated`, `headers.AuthPayload`, the nested signer call, and formatted auth summaries. Pass `authentication.DMARC.Status` into the policy gate and pass `authentication` to screening. Persist `threadInfo.From` as `header_from`, the SMTP envelope address as inbound `envelope_from`, and the authentication object.

Change `EmailReceivedData` to:

```go
HeaderFrom *string `json:"header_from" nullable:"true"`
EnvelopeFrom *string `json:"envelope_from" nullable:"true"`
ReplyTo []string `json:"reply_to" nullable:"false"`
Authentication *emailauth.Authentication `json:"authentication" nullable:"true"`
```

Remove `From`, `AuthenticatedFrom`, and `AuthHeaders`. Update the golden fixtures with the approved object.

- [ ] **Step 5: Run policy, relay, eventpayload, and e2e tests**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/inboundpolicy ./internal/relay ./internal/eventpayload ./internal/piguard -count=1
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable' GOCACHE=/tmp/e2a-go-build-cache go test -tags integration -p 1 ./internal/e2e -run TestInboundPolicy -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit relay and event behavior**

```bash
git add internal/inboundpolicy internal/relay internal/eventpayload internal/piguard internal/e2e/inbound_policy_e2e_test.go
git commit -m "fix(relay): require aligned DMARC for sender trust"
```

## Task 6: REST views, loopback, and WebSocket parity

**Files:**
- Modify: `internal/httpapi/messages.go`
- Modify: `internal/httpapi/outbound.go`
- Modify: `internal/httpapi/outbound_reply_direction_test.go`
- Modify: `internal/httpapi/messages_parsed_test.go`
- Modify: `internal/httpapi/operations_test.go`
- Modify: `internal/httpapi/export_stability_test.go`
- Modify: `internal/httpapi/eventpayload_schemas_test.go`
- Modify: `internal/ws/handler.go`
- Modify: `internal/ws/handler_test.go`
- Modify: `internal/agent/selfsend.go`
- Modify: `internal/agent/selfsend_test.go`

- [ ] **Step 1: Add response-shape tests**

For inbound detail and summary, marshal response views and assert:

```go
for _, key := range []string{"header_from", "envelope_from", "authentication"} {
	if _, ok := body[key]; !ok { t.Fatalf("missing %s", key) }
}
for _, retired := range []string{"from", "authenticated_from", "auth", "auth_headers"} {
	if _, ok := body[retired]; ok { t.Fatalf("retired field %s present", retired) }
}
```

Add outbound and loopback cases asserting the `authentication` key is present with JSON null. Add a reconnect test asserting the WebSocket payload equals the persisted outbox envelope and contains the same authentication object.

- [ ] **Step 2: Run the focused API/WS/loopback tests and verify failure**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/httpapi ./internal/ws ./internal/agent -run 'Authentication|Loopback|Notification' -count=1
```

Expected: FAIL on legacy field names and omitted null authentication.

- [ ] **Step 3: Replace public message fields**

In both `MessageView` and `MessageSummaryView`, replace `From`, `AuthHeaders`, and `Auth` with:

```go
HeaderFrom *string `json:"header_from" nullable:"true"`
EnvelopeFrom *string `json:"envelope_from" nullable:"true"`
Authentication *emailauth.Authentication `json:"authentication" nullable:"true"`
```

Do not use `omitempty` on `authentication`; required JSON null is contractual. Map outbound stored sender to `header_from`, and inbound to the new stored header identity. Update account export schemas consistently.

In `BuildNotification`, stop reconstructing sender identity from auth headers; use `msg.HeaderFrom`, `msg.EnvelopeFrom`, and `msg.Authentication`. In self-send, set `header_from` to the composed agent From, preserve Reply-To separately, and emit `authentication: null`.

Update reply fallback behavior in `internal/httpapi/outbound.go`: when raw MIME
cannot be parsed, target the first stored `ReplyTo` address when present and
fall back to `HeaderFrom` only when Reply-To is empty. Add a regression proving
the field rename does not change reply routing.

- [ ] **Step 4: Run all affected Go unit packages**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/httpapi ./internal/ws ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit API behavior**

```bash
git add internal/httpapi internal/ws internal/agent
git commit -m "feat(api): expose canonical email authentication"
```

## Task 7: Remove nested auth headers and obsolete signer plumbing

**Files:**
- Delete: `internal/headers/signer.go`
- Delete: `internal/headers/signer_test.go`
- Modify: `internal/relay/server.go`
- Modify: `cmd/e2a/main.go`
- Modify: `internal/testutil/server.go`
- Modify: `internal/testutil/contract_server.go`
- Modify: relay and testutil callers returned by the reference audit

- [ ] **Step 1: Prove remaining references before deletion**

Run:

```bash
rg -n 'internal/headers|headers\.Signer|AuthHeaders|auth_headers|X-E2A-Auth-' --glob '!docs/superpowers/**'
```

Expected: only signer plumbing, tests, generated/client/docs surfaces scheduled in Tasks 8–10, and sender-injected-header warning text remain. Any runtime trust consumer must be migrated before continuing.

- [ ] **Step 2: Remove signer injection from the relay**

Change:

```go
func NewServer(cfg *config.Config, store *identity.Store, usage usage.UsageTracker, hub *ws.Hub) *Server
```

Remove `signer` from `relay.Server`, test server structs, and constructor calls in `cmd/e2a/main.go`, `internal/testutil/server.go`, and `internal/testutil/contract_server.go`. Delete the headers package if the second reference audit shows no non-generated runtime imports.

- [ ] **Step 3: Run constructor and relay tests**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./cmd/e2a ./internal/relay ./internal/testutil ./internal/ws ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit dead-code removal**

```bash
git add -A internal/headers internal/relay cmd/e2a internal/testutil internal/ws internal/agent
git commit -m "refactor(auth): remove nested message attestations"
```

## Task 8: OpenAPI and generated SDK contract

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `sdks/typescript/src/v1/generated/`
- Modify: `sdks/python/src/e2a/v1/generated/`
- Modify: `internal/httpapi/export_stability_test.go`

- [ ] **Step 1: Add schema assertions before regeneration**

Update schema tests to require `SPFResult`, `DKIMResult`, `DMARCResult`, and `Authentication`, require `header_from`, `envelope_from`, and `authentication` on message/event schemas, and reject `AuthVerdict`, `CheckResult`, `authenticated_from`, and `auth_headers`.

- [ ] **Step 2: Run the spec drift test and verify failure**

```bash
GOCACHE=/tmp/e2a-go-build-cache go test ./internal/httpapi -run 'TestSpecGoldenNoDrift|TestStableExportSchemas|TestEventPayloadSchemas' -count=1
```

Expected: FAIL because committed OpenAPI and generated schemas still contain retired fields.

- [ ] **Step 3: Regenerate OpenAPI and SDK bases**

Run:

```bash
make generate
```

Expected: `api/openapi.yaml`, TypeScript generated models, and Python generated models change; `AuthVerdict`/`CheckResult` generated files disappear and the four canonical models appear.

- [ ] **Step 4: Verify generated contract freshness**

```bash
make spec-check
make generate-sdk-check
npm test --workspace @e2a/sdk
cd sdks/python && pytest tests/ -v && mypy
```

Expected: all commands PASS.

- [ ] **Step 5: Commit generated artifacts**

```bash
git add api/openapi.yaml sdks/typescript/src/v1/generated sdks/python/src/e2a/v1/generated internal/httpapi
git commit -m "feat(openapi): publish email authentication schema"
```

## Task 9: Hand-written SDK, CLI, and MCP surfaces

**Files:**
- Modify: `sdks/typescript/src/v1/webhook-signature.ts`
- Modify: `sdks/typescript/test/v1/webhook-payloads.test.ts`
- Modify: `sdks/python/src/e2a/v1/webhook_signature.py`
- Modify: `sdks/python/tests/test_webhook_payloads.py`
- Modify: `cli/src/commands/listen.ts`
- Modify: `cli/src/__tests__/listen.test.ts`
- Modify: `mcp/src/tools/messages.ts`
- Modify: MCP message-tool tests located by `rg -l 'get_message' mcp/src --glob '*test*'`

- [ ] **Step 1: Replace webhook fixture expectations**

Assert hand-written event types expose `header_from`, nullable `envelope_from`, and nullable `authentication`, and cannot compile/type-check with `authenticated_from` or `auth_headers`. Update Python `TypedDict` definitions to the same required keys.

- [ ] **Step 2: Run SDK tests and verify failure**

```bash
npm test --workspace @e2a/sdk
cd sdks/python && pytest tests/test_webhook_payloads.py -v
```

Expected: FAIL on retired payload fields.

- [ ] **Step 3: Update SDK payload helpers**

Import the generated `Authentication` model in TypeScript and Python hand-written webhook modules. Remove nested-auth-header documentation and verification language; keep webhook envelope signature verification unchanged.

- [ ] **Step 4: Update CLI forwarding and MCP projection**

Replace CLI `notification.from` with `notification.header_from`; replace the `withWireFrom` compatibility helper with a `header_from` serializer or remove it when generated models serialize the approved wire name directly. Ensure generic forward JSON includes required `authentication`, including null.

Change MCP `get_message` output to include:

```ts
header_from: email.headerFrom,
envelope_from: email.envelopeFrom,
authentication: email.authentication,
```

and remove `from_` from the tool result. Keep reply routing based on `reply_to` with header-From fallback.

- [ ] **Step 5: Run SDK, CLI, and MCP tests**

```bash
npm test --workspace @e2a/sdk
npm test --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
cd sdks/python && pytest tests/ -v && mypy
```

Expected: PASS.

- [ ] **Step 6: Commit client surfaces**

```bash
git add sdks/typescript sdks/python cli mcp
git commit -m "feat(clients): consume canonical authentication fields"
```

## Task 10: Dashboard authentication presentation

**Files:**
- Modify: `web/src/app/components/types.ts`
- Modify: `web/src/app/components/onboarding/api.ts`
- Modify: `web/src/app/components/onboarding/api.test.ts`
- Modify: `web/src/app/(app)/inboxes/(view)/messages/view/page.tsx`
- Modify: `web/src/app/(app)/inboxes/(view)/messages/view/page.test.tsx`
- Modify: `web/src/app/components/messages/MessageLifecycleTimeline.tsx`
- Modify: related web fixtures returned by `rg -l 'auth_headers|authenticated_from|\bfrom:' web/src --glob '*test*'`

- [ ] **Step 1: Add dashboard projection tests**

Update fixtures to the canonical object and assert projections retain `header_from`, `envelope_from`, and the complete authentication object. Add view tests for DMARC pass, fail, none, temperror, and permerror labels; verify SPF/DKIM pass without alignment never renders “authenticated”.

- [ ] **Step 2: Run focused web tests and verify failure**

```bash
cd web
npm test -- --runInBand src/app/components/onboarding/api.test.ts 'src/app/(app)/inboxes/(view)/messages/view/page.test.tsx'
```

Expected: FAIL because dashboard wire types and rendering parse `auth_headers`.

- [ ] **Step 3: Replace dashboard types and rendering**

Add TypeScript UI types matching generated status unions. Rename `MessageSummary.from` and `InboundMessageDetail.from` to `header_from`; add nullable `envelope_from` and `authentication`. Replace the header-entry loop in the message view with structured SPF/DKIM/DMARC rows. Render “Authenticated domain” only for DMARC pass; render distinct neutral/error treatments for none, temperror, and permerror. Update lifecycle copy so it never says “auth verified” without a DMARC pass.

- [ ] **Step 4: Run web tests, lint, and build**

```bash
cd web
npm test -- --runInBand
npm run lint
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit dashboard changes**

```bash
git add web
git commit -m "feat(web): display structured email authentication"
```

## Task 11: Documentation, examples, and agent plugin safety

**Files:**
- Modify: `README.md`
- Modify: `docs/api.md`
- Modify: `docs/events.md`
- Modify: `docs/design/autonomous-repo-framework.md`
- Modify: `plugins/e2a/docs/e2a.md`
- Modify: `plugins/e2a/docs/sdk.md`
- Modify: `plugins/e2a/skills/agentify/references/adapters.md`
- Modify: `plugins/e2a/skills/agentify/references/security-invariants.md`
- Modify: `plugins/e2a/skills/agentify/templates/runtime-skill/triage.md`
- Modify: agentify fixtures under `plugins/e2a/skills/agentify/test/fixtures/`
- Modify: `examples/adk-cloud-webhook/test_live_integration.py`
- Modify: web blog/docs copy returned by the retired-field audit

- [ ] **Step 1: Run the retired-contract audit and save the exact worklist**

```bash
rg -n 'authenticated_from|auth_headers|X-E2A-Auth-|AuthVerdict|CheckResult|\.from\b|"from"' README.md docs plugins examples web/src/app/blog --glob '!docs/superpowers/**'
```

Expected: matches identify every document/fixture that must change; unrelated prose about ordinary email From headers remains after manual classification.

- [ ] **Step 2: Update security guidance and examples**

Replace authorization examples with:

```ts
const trusted =
  message.authentication?.dmarc.status === "pass" &&
  message.header_from?.toLowerCase() === configuredAddress.toLowerCase();
```

State explicitly that this proves domain-authorized use of the From address, not a human identity. Replace event examples with `header_from`, `envelope_from`, and the authentication object. Remove all instructions to parse or verify nested `X-E2A-Auth-*` fields.

- [ ] **Step 3: Update agentify fixtures and assertions**

Change mock MCP messages and triage fixtures to carry `header_from` plus a DMARC pass object. Add a spoof fixture whose address matches but DMARC status is fail, and assert triage refuses to treat it as a verified approval.

- [ ] **Step 4: Run plugin and repository integrity checks**

```bash
node scripts/validate-plugin.mjs
bash scripts/check-repository-text-integrity.sh
rg -n 'authenticated_from|auth_headers|X-E2A-Auth-|AuthVerdict|CheckResult' --glob '!docs/superpowers/**' --glob '!migrations/**'
```

Expected: validation and text-integrity commands PASS; the final `rg` returns no public/runtime references. Historical migration comments may remain.

- [ ] **Step 5: Commit docs and plugin updates**

```bash
git add README.md docs plugins examples web/src/app/blog
git commit -m "docs(api): document DMARC authentication contract"
```

## Task 12: Full verification and compatibility review

**Files:**
- Modify only files required by failures found in this task.

- [ ] **Step 1: Run formatting and focused race-sensitive checks**

```bash
gofmt -w internal/emailauth internal/identity internal/inboundpolicy internal/relay internal/eventpayload internal/httpapi internal/ws internal/agent cmd/e2a
GOCACHE=/tmp/e2a-go-build-cache go test -race ./internal/relay ./internal/emailauth
```

Expected: formatting produces no unintended diff and race tests PASS.

- [ ] **Step 2: Run the complete Go suite serially**

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable' GOCACHE=/tmp/e2a-go-build-cache go test -tags integration -p 1 ./...
```

Expected: PASS.

- [ ] **Step 3: Run every non-Go surface**

```bash
npm test --workspace @e2a/sdk
npm test --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
npm run build --workspace @e2a/sdk
npm run build --workspace @e2a/cli
npm run build --workspace @e2a/mcp-server
cd sdks/python && pytest tests/ -v && mypy
cd ../../web && npm test -- --runInBand && npm run lint && npm run build
```

Expected: every command PASS.

- [ ] **Step 4: Run contract, generation, and repository gates**

```bash
make spec-check
make generate-sdk-check
make openapi-compat-check
node scripts/check-sdk-version-sync.mjs
bash scripts/check-repository-text-integrity.sh
```

Expected: spec/generation/version/text gates PASS. `openapi-compat-check` is expected to report the intentionally approved breaking rename/removal against `origin/main`; capture that report in the PR and verify it contains no unapproved breaking change.

- [ ] **Step 5: Perform final retired-field and outbound-SES audits**

```bash
rg -n 'authenticated_from|auth_headers|X-E2A-Auth-|AuthVerdict|CheckResult|DomainAuthenticated' --glob '!docs/superpowers/**' --glob '!migrations/**'
git diff origin/main -- internal/outbound internal/outboundsend internal/senderidentity internal/delivery
```

Expected: no retired runtime/public references; the outbound/SES diff is empty except type-renaming adaptations required to compile message reads. SMTP submission, BYODKIM, MAIL FROM, SES feedback, suppression, and send-ramp behavior remain unchanged.

- [ ] **Step 6: Commit verification fixes, if any**

If verification required changes, inspect `git status --short`, stage exactly
those reported fix files, and commit:

```bash
git add internal api sdks cli mcp web README.md docs plugins examples migrations cmd
git commit -m "test(api): verify authentication contract parity"
```

If no files changed, do not create an empty commit.
