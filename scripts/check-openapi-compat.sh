#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <base-openapi> [revision-openapi]" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base="$1"
revision="${2:-$repo_root/api/openapi.yaml}"
levels="$repo_root/api/oasdiff-levels.txt"
ignored_errors="$repo_root/api/oasdiff-ignore-errors.txt"
ignored_warnings="$repo_root/api/oasdiff-ignore-warnings.txt"
format="${OASDIFF_FORMAT:-text}"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/e2a-openapi-compat.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

materialize() {
  local source="$1"
  local destination="$2"
  if [[ -f "$source" ]]; then
    cp "$source" "$destination"
  elif git -C "$repo_root" cat-file -e "$source" 2>/dev/null; then
    git -C "$repo_root" show "$source" >"$destination"
  elif [[ "$source" == https://* || "$source" == http://* ]]; then
    curl --fail --silent --show-error --location "$source" >"$destination"
  else
    echo "OpenAPI input is not a file, Git object, or HTTP(S) URL: $source" >&2
    exit 2
  fi
}

normalize() {
  local source="$1"
  local raw="$2"
  local destination="$3"
  materialize "$source" "$raw"
  (cd "$repo_root" && GOWORK=off go run ./cmd/e2a-openapi-normalize "$raw" "$destination")
}

normalize "$base" "$tmpdir/base.raw.yaml" "$tmpdir/base.yaml"
normalize "$revision" "$tmpdir/revision.raw.yaml" "$tmpdir/revision.yaml"
(cd "$repo_root" && GOWORK=off go run ./cmd/e2a-openapi-security-check "$tmpdir/base.yaml" "$tmpdir/revision.yaml")

run_oasdiff() {
  if [[ -n "${OASDIFF_BIN:-}" ]]; then
    local binary metadata
    binary="$(command -v "$OASDIFF_BIN" 2>/dev/null || true)"
    if [[ -z "$binary" ]]; then
      echo "OASDIFF_BIN is not executable: $OASDIFF_BIN" >&2
      exit 2
    fi
    metadata="$(go version -m "$binary" 2>/dev/null || true)"
    if ! grep -Fq $'\tmod\tgithub.com/oasdiff/oasdiff\tv1.23.0\t' <<<"$metadata"; then
      echo "OASDIFF_BIN must be github.com/oasdiff/oasdiff v1.23.0: $binary" >&2
      exit 2
    fi
    "$binary" "$@"
    return
  fi
  GOWORK=off go run github.com/oasdiff/oasdiff@v1.23.0 "$@"
}

# The canonical x-stability-level marker lets the stable threshold exclude beta
# operations even when an entire beta path has been removed from the revision.
# Historical x-stability: experimental markers were translated during the
# normalization step above.
run_oasdiff breaking \
  --allow-external-refs=false \
  --fail-on WARN \
  --stability-level stable \
  --severity-levels "$levels" \
  --err-ignore "$ignored_errors" \
  --warn-ignore "$ignored_warnings" \
  --format "$format" \
  "$tmpdir/base.yaml" "$tmpdir/revision.yaml"

# oasdiff findings take precedence when both the wire contract and generated
# SDK surface change. If the wire contract is compatible, freeze the stable
# component model names and operation grouping used by SDK generators.
(cd "$repo_root" && GOWORK=off go run ./cmd/e2a-openapi-sdk-check "$tmpdir/base.yaml" "$tmpdir/revision.yaml")
