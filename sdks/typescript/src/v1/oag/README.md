# `oag/` — OpenAPI-Generator `/v1` client base (generated)

Do not edit by hand. Regenerate with `make generate-sdk-ts`
(`sdks/typescript/scripts/generate-oag.sh`): OpenAPI Generator's `typescript`
generator against the canonical `api/openapi.yaml` (pinned image
`openapitools/openapi-generator-cli:v7.16.0`), then `.js` appended to relative
imports for Node16/ESM.

Generated transport + models + typed `ApiException<ErrorEnvelope>`. The
hand-written ergonomic layer (`client.ts`, `ws.ts`, `webhook-signature.ts`,
`inbound-email.ts`) wraps it — api-v1-redesign **Slice 8**. Staged now; wired
into the published client in the next slice.
