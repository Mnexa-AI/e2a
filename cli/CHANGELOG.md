# Changelog

## 1.5.0

Current release. The `e2a` CLI targets the e2a v1 API and runs on the 3.x
TypeScript SDK (`@e2a/sdk`).

### Notes
- Commands are thin wrappers over the namespaced SDK surface
  (`client.agents`, `client.messages`, `client.domains`, `client.webhooks`,
  `client.account`). Auth reads `E2A_API_KEY`; `--base-url` (or `E2A_BASE_URL`)
  overrides the endpoint for self-hosted deployments.
