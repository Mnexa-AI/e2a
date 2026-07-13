#!/usr/bin/env bash
# Regenerate the Python /v1 client base from the canonical api/openapi.yaml
# using OpenAPI Generator's `python` generator with the **httpx** library:
# async-native (matches the async-only Python decision) and httpx-based (the
# same HTTP client the hand-written layer uses — no second HTTP dependency).
# Output lands as the package e2a.v1.generated; the hand-written ergonomic layer wraps it.
# Pinned image tag → reproducible for the drift gate. Run via Docker (no Java).
#
# packageName=e2a.v1.generated so the generator's absolute imports (`from
# e2a.v1.generated ...`) match the package's final location. We generate to a
# temp dir and copy only the leaf package, so nothing pollutes the source root
# and the existing e2a/__init__.py and e2a/v1/__init__.py are never touched.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
TMP="$ROOT/sdks/python/.oag-tmp"
DEST="$ROOT/sdks/python/src/e2a/v1/generated"
IMG="openapitools/openapi-generator-cli:v7.16.0"

rm -rf "$TMP"
# Run as the invoking host user (not the container's default root) so the
# generated files + the .oag-tmp scratch dir are host-user-owned and removable
# on CI's non-root runner. HOME is a writable path for tools that expect it.
# (Docker Desktop/macOS maps ownership already, so this is a no-op there but
# required on Linux CI.)
# --name-mappings / --parameter-name-mappings: the wire field `from` is a
# Python keyword, which the generator would otherwise escape to the ugly
# `var_from`. Map it to the PEP-8-standard `from_` (trailing underscore), the
# same spelling the hand-written layer already uses for request-side params,
# so the SDK teaches exactly one spelling. Wire JSON stays `from` (the pydantic
# alias / query-param name are unchanged).
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$ROOT:/work" "$IMG" generate \
  -i /work/api/openapi.yaml -g python \
  -o /work/sdks/python/.oag-tmp \
  --name-mappings from=from_ --parameter-name-mappings from=from_ \
  --additional-properties=packageName=e2a.v1.generated,library=httpx >/dev/null

rm -rf "$DEST"
cp -r "$TMP/e2a/v1/generated" "$DEST"
rm -rf "$TMP"

# Strip the generator's `*_validate_enum` validators so the client tolerates
# unknown enum values (forward-compat: a new server enum value must not crash a
# deployed client). Matches the TypeScript SDK's passthrough behavior.
python3 "$ROOT/sdks/python/scripts/strip-enum-validators.py" "$DEST"

echo "Python /v1 client base regenerated at sdks/python/src/e2a/v1/generated"
