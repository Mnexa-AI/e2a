# Generic OIDC Login Design

## Problem statement

PR #536 currently accepts a signed JWT in
`GET /api/auth/external/callback?assertion=...`. Although it validates the
signature and core claims, that is not a safe browser sign-in protocol: the
credential can leak through browser history, logs, and referrers; it is
replayable for its lifetime; and the callback is not bound to a login initiated
by this e2a deployment.

TokenCanopy needs a vendor-neutral way to sign a person into an existing e2a
account while e2a remains usable as an independent OSS deployment. Existing
e2a user IDs must remain stable, and legacy Google login must continue to work
during migration.

## Goals and non-goals

Goals:

- Support any standards-compliant OpenID Connect provider, including a
  TokenCanopy identity broker backed by WorkOS or Auth0.
- Use the OIDC Authorization Code flow with PKCE, state, and nonce.
- Resolve a configurable ID-token claim, `e2a_user_id` for TokenCanopy, to an
  existing e2a `users.id` and create the normal e2a session.
- Keep the integration optional and off by default.
- Preserve Google login, CLI and agent authentication, and existing user data.

Non-goals:

- Provisioning new e2a users from OIDC claims.
- Matching accounts by mutable email address.
- Replacing e2a's session cookie or legacy Google login in this PR.
- Implementing TokenCanopy's OIDC provider endpoints in this repository.
- Adding organization or workspace authorization to e2a.

## Approaches considered

### Signed assertion in the callback URL

This is the current PR. It is small, but it creates a custom login protocol and
puts a reusable bearer credential in a URL. Adding `jti` storage and a POST
handoff would reduce replay and leakage, but e2a and TokenCanopy would still own
a bespoke security protocol.

### One-time authorization code

TokenCanopy could issue a short-lived, single-use opaque code and e2a could
redeem it over a back channel. This is substantially safer than the current PR,
but duplicates a subset of OAuth/OIDC and makes other identity providers require
an adapter.

### OpenID Connect Authorization Code flow

Use provider discovery, a standard authorization endpoint, back-channel token
exchange, PKCE S256, state, nonce, and ID-token verification. This is the
recommended design because it is interoperable, uses mature libraries, and
keeps bearer tokens out of browser URLs.

## Configuration

Add an optional `oidc` configuration block with environment overrides:

- `E2A_OIDC_ENABLED`: registers the OIDC routes only when true.
- `E2A_OIDC_ISSUER_URL`: exact expected issuer and discovery base URL.
- `E2A_OIDC_CLIENT_ID`: audience expected in the ID token.
- `E2A_OIDC_CLIENT_SECRET`: confidential-client credential used only at the
  token endpoint.
- `E2A_OIDC_REDIRECT_URL`: absolute callback URL registered with the provider.
- `E2A_OIDC_USER_ID_CLAIM`: non-empty string claim containing an existing e2a
  user ID; TokenCanopy uses `e2a_user_id`.

All fields are required when enabled. Disabling the feature requires none of
them and leaves the routes absent. Scopes are fixed to `openid`; the integration
does not require email or profile data.

## Routes and protocol

### `GET /api/auth/oidc/login`

1. Generate independent cryptographically random values for OAuth state, OIDC
   nonce, and the PKCE verifier.
2. Store them in short-lived, HttpOnly, SameSite=Lax cookies. Set Secure in
   production. Scope the transaction cookies to the OIDC route prefix and
   expire them after ten minutes.
3. Redirect to the discovered authorization endpoint with `response_type=code`,
   `scope=openid`, the configured client and redirect URI, state, nonce, and a
   PKCE S256 challenge.

The transaction cookies contain only random values and never identity data.
Separate cookies avoid introducing a new server-side transaction table or a new
cookie-encryption key. Their high entropy, same-origin cookie protections, exact
state comparison, PKCE binding, and nonce validation jointly bind the callback
to the initiating browser and authorization request.

### `GET /api/auth/oidc/callback`

1. Reject provider errors, a missing code or state, missing transaction cookies,
   or a state mismatch.
2. Delete all transaction cookies before exchanging the code so the browser
   cannot reuse the login transaction.
3. Exchange the code at the discovered token endpoint using the client secret,
   redirect URI, and PKCE verifier.
4. Require an `id_token` in the token response.
5. Verify its signature against the provider's discovered JWKS and validate the
   exact issuer, client audience, expiry, and nonce. Provider key rotation is
   delegated to the OIDC verifier library.
6. Read the configured user-ID claim as a non-empty string and look it up with
   `GetUserByID`. Reject an unknown ID; never create or email-match a user.
7. Create the existing e2a session, set the normal `e2a_session` cookie, and
   redirect to `/dashboard` on e2a's configured frontend origin.

All callback failures return a generic client response and log the detailed
reason server-side without logging codes, tokens, client secrets, or cookie
values.

## Existing-user migration contract

TokenCanopy remains responsible for linking identities and preserving existing
accounts. For every migrated e2a account it stores the unchanged e2a
`users.id`. After TokenCanopy authenticates the person and resolves that link,
its OIDC ID token includes:

```json
{
  "iss": "https://<tokencanopy-issuer>",
  "aud": "<e2a-oidc-client-id>",
  "sub": "<stable-tokencanopy-principal-id>",
  "nonce": "<request-nonce>",
  "e2a_user_id": "<unchanged-e2a-users.id>"
}
```

e2a trusts the configured issuer to make that mapping but still requires the
local user row to exist. The ID-token subject is the stable TokenCanopy
principal; it is not substituted for the e2a user ID. This lets an existing
user sign into TokenCanopy and enter e2a in one redirect journey without
migrating e2a-owned mailboxes, agents, messages, API keys, or session records.

TokenCanopy must expose a standards-compliant discovery document,
authorization endpoint, token endpoint, and JWKS endpoint, or use a provider
feature that does so. Its current raw signed-JWT launch endpoint is not the
contract consumed by this design.

## Component changes

- Replace `internal/auth/external.go` with an OIDC relying-party implementation
  built on `golang.org/x/oauth2` and a maintained OIDC verifier library.
- Replace assertion/JWKS tests with an in-process fake OIDC provider covering
  discovery, authorization parameters, token exchange, and signed ID tokens.
- Rename `ExternalAuthConfig` and its config surface to `OIDCConfig`.
- Register both OIDC routes only when configuration is enabled and provider
  discovery succeeds at startup.
- Update `config.example.yaml` and security documentation where relevant.
- Leave `UserAuth` and all Google routes unchanged.

## Failure handling

- Discovery or invalid enabled configuration: fail startup with a useful error.
- Authorization endpoint unavailable: the provider owns its error page; e2a
  has not created a session.
- Callback state or transaction-cookie failure: reject before token exchange.
- Token exchange, ID-token, nonce, or claim failure: reject without a session.
- Unknown e2a user ID: return forbidden and never provision a user.
- Database/session failure: return an internal error without exposing provider
  credentials or token contents.

## Verification strategy

- Configuration tests for disabled mode and every required enabled field.
- Login tests for state, nonce, PKCE S256, redirect URI, client ID, and scope.
- Callback tests for valid login and session creation; state mismatch; missing
  or consumed cookies; provider error; missing code; failed exchange; missing ID
  token; invalid signature, issuer, audience, expiry, or nonce; malformed or
  missing user-ID claim; and unknown local user.
- Route tests proving both endpoints are absent when disabled.
- Regression tests proving the legacy Google routes still work.
- Focused auth/config tests, then the complete Go test suite and repository
  checks required by CI.

## Rollout and rollback

Deploy the provider endpoints and e2a client registration first. Enable OIDC on
a staging e2a deployment, verify a sample of preprovisioned existing users, then
enable it in production while retaining Google login. Monitor callback failure
categories without logging secrets. Rollback is configuration-only: disable
OIDC and existing Google login and e2a sessions continue to work.

## Open questions

None for the e2a relying-party boundary. TokenCanopy's provider implementation
is a separate change and must satisfy the contract above before this path can be
enabled.
