#!/usr/bin/env bash
# Regenerate the Python /v1 client base from the canonical api/openapi.yaml
# using OpenAPI Generator's `python` generator with the **httpx** library:
# async-native (matches the async-only Python decision) and httpx-based (the
# same HTTP client the hand-written layer uses — no second HTTP dependency).
# Output lands as the package e2a.v1.oag; the hand-written ergonomic layer wraps it.
# Pinned image tag → reproducible for the drift gate. Run via Docker (no Java).
#
# packageName=e2a.v1.oag so the generator's absolute imports (`from e2a.v1.oag
# ...`) match the package's final location. We generate to a temp dir and copy
# only the leaf package, so nothing pollutes the source root and the existing
# e2a/__init__.py and e2a/v1/__init__.py are never touched.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
TMP="$ROOT/sdks/python/.oag-tmp"
DEST="$ROOT/sdks/python/src/e2a/v1/oag"
IMG="openapitools/openapi-generator-cli:v7.16.0"

rm -rf "$TMP"
docker run --rm -v "$ROOT:/work" "$IMG" generate \
  -i /work/api/openapi.yaml -g python \
  -o /work/sdks/python/.oag-tmp \
  --additional-properties=packageName=e2a.v1.oag,library=httpx >/dev/null

rm -rf "$DEST"
cp -r "$TMP/e2a/v1/oag" "$DEST"
rm -rf "$TMP"
echo "Python /v1 client base regenerated at sdks/python/src/e2a/v1/oag"
