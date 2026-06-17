#!/usr/bin/env bash
# Regenerate the TypeScript /v1 client base from the canonical api/openapi.yaml
# using OpenAPI Generator's `typescript` generator (NOT typescript-fetch, which
# fails TS2590 on wide models — see api-v1-redesign Slice 8). Output lands in
# sdks/typescript/src/v1/oag/; the hand-written ergonomic layer wraps it.
# Pinned image tag → reproducible for the drift gate. Run via Docker (no Java).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="$ROOT/sdks/typescript/src/v1/oag"
IMG="openapitools/openapi-generator-cli:v7.16.0"

# Clean prior output but keep .openapi-generator-ignore (suppresses scaffolding).
find "$OUT" -name '*.ts' -delete 2>/dev/null || true

docker run --rm -v "$ROOT:/work" "$IMG" generate \
  -i /work/api/openapi.yaml -g typescript \
  -o /work/sdks/typescript/src/v1/oag >/dev/null

# Node16/ESM requires explicit extensions on relative specifiers; OpenAPI
# Generator emits none. Append .js to every relative import/export. Runs once on
# fresh (extensionless) output, so it never double-applies.
find "$OUT" -name '*.ts' -print0 | xargs -0 perl -i -pe \
  's/(\bfrom\s+)(["'"'"'])(\.\.?\/[^"'"'"']+)\2/$1$2$3.js$2/g'

echo "TS /v1 client base regenerated at sdks/typescript/src/v1/oag"
