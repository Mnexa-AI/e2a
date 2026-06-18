#!/usr/bin/env bash
# Regenerate the TypeScript /v1 client base from the canonical api/openapi.yaml
# using OpenAPI Generator's `typescript` generator (NOT typescript-fetch, which
# fails TS2590 on wide models — see api-v1-redesign Slice 8). Output lands in
# sdks/typescript/src/v1/generated/; the hand-written ergonomic layer wraps it.
# Pinned image tag → reproducible for the drift gate. Run via Docker (no Java).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="$ROOT/sdks/typescript/src/v1/generated"
IMG="openapitools/openapi-generator-cli:v7.16.0"

# Clean prior output but keep .openapi-generator-ignore (suppresses scaffolding).
find "$OUT" -name '*.ts' -delete 2>/dev/null || true

# importFileExtension=.js → emit ESM-correct `.js` relative imports directly, so
#                      no extension post-process is needed (Node16/NodeNext).
# (Default platform: its fetch call uses a STANDARD RequestInit — no node-fetch
#  `agent`/`buffer` — so it is native-fetch-compatible once the polyfill import
#  is stripped below.)
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$ROOT:/work" "$IMG" generate \
  -i /work/api/openapi.yaml -g typescript \
  -o /work/sdks/typescript/src/v1/generated \
  --additional-properties=supportsES6=true,importFileExtension=.js >/dev/null

# Use the runtime's native global fetch (Node 18+, browsers, edge/Workers) — strip
# the generator's `whatwg-fetch` polyfill import so the SDK carries no fetch
# dependency and stays universal. Re-applied on every regen.
find "$OUT" -name '*.ts' -print0 | xargs -0 perl -i -ne \
  'print unless /^\s*import\s+["'"'"']whatwg-fetch["'"'"'];\s*$/'

echo "TS /v1 client base regenerated at sdks/typescript/src/v1/generated"
