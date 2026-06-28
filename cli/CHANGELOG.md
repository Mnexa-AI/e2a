# Changelog

## 1.5.1

Current release. Republishes the CLI with a corrected `@e2a/sdk` dependency
range (`^4.0.0`); the previously published 1.5.0 still declared `^3.0.0`, so a
fresh `npm i -g @e2a/cli` could resolve an SDK major incompatible with the
current API. No CLI behavior changes.

## 1.5.0

The `e2a` CLI targets the e2a v1 API and runs on the 4.x TypeScript SDK
(`@e2a/sdk`).

### Notes
- Commands are thin wrappers over the namespaced SDK surface
  (`client.agents`, `client.messages`, `client.domains`, `client.webhooks`,
  `client.account`). Auth reads `E2A_API_KEY`; `E2A_URL` overrides the endpoint
  for self-hosted deployments (default `https://e2a.dev`, the hosted product's
  unified host — it serves the `e2a login` browser flow and proxies the `/v1`
  API). Direct SDK users (no browser login) can point at the API host
  `https://api.e2a.dev` instead.
