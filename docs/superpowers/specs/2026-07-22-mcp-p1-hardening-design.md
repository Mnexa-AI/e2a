# MCP P1 Hardening Design

## Goal

Fix three remaining P1 defects in the hosted MCP surface: `get_message` is
incorrectly advertised as read-only, deployed servers report a stale npm
version, and unauthenticated callers can make Express parse the full 40 MB MCP
request allowance before their bearer is rejected.

## Tool metadata

`get_message` marks unread inbound mail as read, so it must not carry
`readOnlyHint: true`. Remove only that hint and retain the existing description
that documents the side effect. The tool-catalog test will explicitly group
`get_message` with non-read-only tools so future annotation cleanup cannot
reintroduce the false promise.

## Server version identity

The hosted MCP server is co-versioned with the product, not with the retired
npm package. The version resolver uses `MCP_SERVER_VERSION` when supplied by a
deployment and otherwise reads the repository's root `VERSION` file. It never
falls back to `mcp/package.json`.

The image workflow computes a value for every build:

- product tag `vX.Y.Z` reports `X.Y.Z`;
- MCP-only tag `mcp-http-vX.Y.Z` reports `X.Y.Z`;
- `main` and manual non-tag builds report `<VERSION>+sha.<12-character SHA>`.

The Docker build receives that value as `MCP_SERVER_VERSION` and persists it in
the runtime image. The root `VERSION` file is also copied so direct/local image
builds have a truthful fallback. `mcp/server.json` remains a release manifest,
so its version matches root `VERSION` rather than a deployment-specific SHA.

## Authenticate before body parsing

Keep Host validation first. For `POST /mcp`, extract and validate the bearer,
including the cached/remote `whoami` resolution, before invoking the route-local
40 MB JSON parser. Once authenticated, attach the bearer and resolved principal
to `res.locals`, parse JSON, and dispatch through the existing stateless MCP
transport.

Pre-parse 401 responses necessarily use JSON-RPC `id: null`, since reading an
attacker-controlled body merely to recover its request id would defeat the
hardening. Authenticated requests retain the current 40 MB attachment envelope
and request-id behavior. Discovery, health, GET/DELETE `/mcp`, scope gating,
and backend authorization remain unchanged.

## Tests and non-goals

Tests cover the corrected annotation, root/env version precedence and live
handshake, workflow/Docker version wiring, missing and invalid bearer rejection
without JSON parsing, and successful parsing of a body larger than 1 MB after
authentication. This change does not add stateful sessions or `listChanged`,
alter attachment limits, change message read semantics, or revive MCP npm
publishing.
