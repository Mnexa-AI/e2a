# Generic OIDC Login Implementation Plan

> **Execution note:** Implement each task in order with failing tests before
> production changes. Preserve the existing Google OAuth flow and never create
> users from OIDC claims.

**Goal:** Rewrite PR #536 from a replayable JWT-in-query callback into an
optional, vendor-neutral OIDC Authorization Code + PKCE login for existing e2a
users.

**Architecture:** e2a is an OIDC relying party. Startup performs provider
discovery and constructs an OAuth client plus ID-token verifier. Browser login
state, nonce, and PKCE verifier live in short-lived HttpOnly cookies. A verified
ID-token claim maps to an unchanged local `users.id`, after which e2a creates
its normal session.

**Tech stack:** Go `net/http`, `golang.org/x/oauth2`,
`github.com/coreos/go-oidc/v3/oidc`, Gorilla mux, PostgreSQL test store, and an
in-process fake OIDC provider.

---

## Task 1: Replace the configuration contract

**Files:**

- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

1. Rewrite the external-auth config tests to expect `OIDCConfig`, the `oidc`
   YAML block, and `E2A_OIDC_*` environment variables.
2. Cover disabled defaults, all environment overrides, every required field,
   accepted complete configuration, and ignored empty fields while disabled.
3. Run `go test ./internal/config` and confirm the renamed tests fail to compile.
4. Replace `ExternalAuthConfig` with fields for enabled, issuer URL, client ID,
   client secret, redirect URL, and user-ID claim. Add environment loading and
   enabled-mode validation.
5. Run `go test ./internal/config` and confirm it passes.

## Task 2: Specify the OIDC relying-party behavior in tests

**Files:**

- Replace: `internal/auth/external_test.go`
- Replace: `internal/auth/external.go`

1. Build a fake provider serving discovery, JWKS, authorization, and token
   endpoints with a generated RSA key.
2. Add constructor tests for disabled mode and discovery failure.
3. Add login tests for state/nonce cookies, PKCE S256, fixed `openid` scope,
   redirect URI, and client ID.
4. Add callback tests for a valid code establishing an e2a session for the
   claimed existing user.
5. Add rejection tests for state mismatch, missing/consumed transaction
   cookies, provider errors, missing code, failed exchange, missing ID token,
   bad signature, issuer, audience, expiry, nonce, user claim, and unknown user.
6. Run `go test ./internal/auth` and confirm failures are caused by the missing
   OIDC implementation.

## Task 3: Implement OIDC login and callback

**Files:**

- Replace: `internal/auth/external.go`
- Modify: `go.mod`
- Modify: `go.sum`

1. Add the maintained `coreos/go-oidc/v3` verifier dependency.
2. Implement an optional `OIDCAuth` constructor that performs discovery and
   returns an error when enabled discovery fails.
3. Implement cryptographically random state/nonce generation, PKCE S256, and
   ten-minute HttpOnly transaction cookies.
4. Implement callback validation, early transaction-cookie deletion,
   back-channel code exchange, ID-token verification, nonce comparison, claim
   extraction, existing-user lookup, normal session creation, and redirect.
5. Ensure logs never include codes, tokens, secrets, or cookie contents.
6. Run `go test ./internal/auth` until all OIDC tests pass.

## Task 4: Wire only the optional routes

**Files:**

- Modify: `internal/agent/api.go`
- Modify: `internal/agent/api_test.go`
- Modify: `cmd/e2a/main.go`

1. Add route tests proving `/api/auth/oidc/login` and
   `/api/auth/oidc/callback` are absent without OIDC wiring and present with it.
2. Rename API wiring from `ExternalAuth` to `OIDCAuth` and register both routes
   only for a non-nil instance.
3. Construct OIDC at startup, fail startup on enabled discovery errors, and log
   only the configured issuer URL when enabled.
4. Run `go test ./internal/agent ./internal/auth`.

## Task 5: Update operator documentation and remove the old protocol

**Files:**

- Modify: `config.example.yaml`
- Verify: `SECURITY.md`
- Delete all remaining references to `external_auth`, assertion query
  parameters, standalone JWKS configuration, and `ExternalAuth`.

1. Document the OIDC fields, routes, existing-user-only behavior, and
   TokenCanopy `e2a_user_id` claim example.
2. Search the repository for stale old-protocol names and remove them.
3. Run formatting and `git diff --check`.

## Task 6: Verify and publish the rewritten PR

1. Run focused tests:
   `go test ./internal/config ./internal/auth ./internal/agent`.
2. Run the complete Go suite with the repository's supported test command.
3. Run any build, generated-code, or lint checks relevant to changed Go files.
4. Inspect the complete diff against `origin/main`, ensuring the raw assertion
   endpoint is gone and unrelated changes are preserved.
5. Commit the implementation with an OIDC-specific message.
6. Push `feat/external-auth-issuer` and update PR #536's title/body to describe
   the new OIDC protocol, migration contract, tests, and rollout dependency on
   TokenCanopy's provider endpoints.
