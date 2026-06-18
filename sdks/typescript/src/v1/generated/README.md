# `generated/` — OpenAPI-Generator `/v1` client base (generated)

Do not edit by hand. Regenerate with `make generate-sdk-ts`
(`sdks/typescript/scripts/generate-oag.sh`): OpenAPI Generator's `typescript`
generator against the canonical `api/openapi.yaml` (pinned image
`openapitools/openapi-generator-cli:v7.16.0`, `importFileExtension=.js` for
native ESM imports). The generator's `whatwg-fetch` polyfill import is stripped
so the base uses the runtime's native global `fetch` (Node 18+, browsers,
edge/Workers) — no fetch dependency.

Generated transport + models + typed `ApiException<ErrorEnvelope>`. The
hand-written ergonomic layer (`client.ts`, `ws.ts`, `webhook-signature.ts`,
`inbound-email.ts`) wraps it — api-v1-redesign **Slice 8**. Staged now; wired
into the published client in the next slice.
