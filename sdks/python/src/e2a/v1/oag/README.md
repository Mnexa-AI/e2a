# `e2a.v1.oag` — OpenAPI-Generator `/v1` client base (generated)

Do not edit by hand. Regenerate with `make generate-sdk-py`
(`sdks/python/scripts/generate-oag.sh`): OpenAPI Generator's `python` generator
with the **httpx** library (async-native; matches the async-only Python SDK and
the hand-written layer's HTTP client), pinned image
`openapitools/openapi-generator-cli:v7.16.0`, against the canonical
`api/openapi.yaml`.

Generated transport + pydantic-v2 models + a status-mapped exception hierarchy.
The hand-written ergonomic layer wraps it — see `docs/design/sdk-interface-v1.md`
**Slice 8**. Staged now; wired into the published client in 8c.
