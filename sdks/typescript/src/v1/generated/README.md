# `generated/` — OpenAPI-Generator `/v1` client base (generated)

Do not edit by hand. Regenerate with `make generate-sdk-ts`
(`sdks/typescript/scripts/generate-oag.sh`): OpenAPI Generator's `typescript`
generator against the canonical `api/openapi.yaml` (pinned image
`openapitools/openapi-generator-cli:v7.16.0`, `importFileExtension=.js` for
native ESM imports). The generator's `whatwg-fetch` polyfill import is stripped
so the base uses the runtime's native global `fetch` (Node 18+, browsers,
edge/Workers) — no fetch dependency.

Generated transport + models + typed `ApiException<ErrorEnvelope>`. The
hand-written ergonomic layer (`client.ts`, `ws.ts`, `webhook-signature.ts`)
wraps it — api-v1-redesign **Slice 8**. Wired into the published `E2AClient`;
the legacy hand-written `api.ts` / `inbound-email.ts` surface and the old
swag-generated types have been retired in favor of this.
