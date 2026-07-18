#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-openapi-compat.sh"
fixtures="$repo_root/api/testdata/oasdiff"

if [[ ! -x "$checker" ]]; then
  echo "missing executable compatibility checker: $checker" >&2
  exit 1
fi

expect_pass() {
  local name="$1"
  local base="$2"
  local revision="$3"
  if ! "$checker" "$base" "$revision" >/dev/null; then
    echo "expected compatibility case to pass: $name" >&2
    exit 1
  fi
}

expect_fail() {
  local name="$1"
  local base="$2"
  local revision="$3"
  local finding="$4"
  local output status
  set +e
  output="$("$checker" "$base" "$revision" 2>&1)"
  status=$?
  set -e
  if [[ $status -eq 0 ]]; then
    echo "expected breaking case to fail: $name" >&2
    exit 1
  fi
  if ! grep -Fq "[$finding]" <<<"$output"; then
    echo "breaking case '$name' failed without expected finding [$finding]" >&2
    echo "$output" >&2
    exit 1
  fi
}

expect_pass "identical contract" "$fixtures/base.yaml" "$fixtures/base.yaml"
expect_pass "additive response field" "$fixtures/base.yaml" "$fixtures/additive-response.yaml"
expect_pass "experimental operation removal" "$fixtures/base.yaml" "$fixtures/experimental-removed.yaml"
expect_pass "beta SDK schema rename" "$fixtures/sdk-base.yaml" "$fixtures/sdk-beta-schema-renamed.yaml"
# The account export's versioned-interior exemption: interior record shapes
# are versioned by UserExport.schema_version, not gated; the envelope and any
# schema shared with the stable surface remain fully gated.
expect_pass "export interior schema change" "$fixtures/export-base.yaml" "$fixtures/export-interior-changed.yaml"

expect_fail "stable operation removal" "$fixtures/base.yaml" "$fixtures/stable-removed.yaml" "api-path-removed-without-deprecation"
expect_fail "operationId rename" "$fixtures/base.yaml" "$fixtures/operation-id-renamed.yaml" "api-operation-id-removed"
expect_fail "new required request field" "$fixtures/base.yaml" "$fixtures/required-request-field.yaml" "new-required-request-property"
expect_fail "new request maxLength" "$fixtures/base.yaml" "$fixtures/request-property-max-length-set.yaml" "request-property-max-length-set"
expect_fail "warning-level request removal" "$fixtures/base.yaml" "$fixtures/request-property-removed.yaml" "request-property-removed"
expect_fail "stable operation marked beta" "$fixtures/base.yaml" "$fixtures/stability-decreased.yaml" "api-stability-decreased"
expect_fail "export envelope key removal" "$fixtures/export-base.yaml" "$fixtures/export-envelope-key-removed.yaml" "response-required-property-removed"
expect_fail "schema shared between export and stable surface" "$fixtures/export-base.yaml" "$fixtures/export-shared-schema-changed.yaml" "response-required-property-removed"
expect_fail "bearer mechanism changed" "$fixtures/security-base.yaml" "$fixtures/security-scheme-changed.yaml" "security-schemes-changed"
expect_fail "stable SDK schema rename" "$fixtures/sdk-base.yaml" "$fixtures/sdk-stable-schema-renamed.yaml" "stable-sdk-schema-removed"
expect_fail "stable operation tag change" "$fixtures/sdk-base.yaml" "$fixtures/sdk-operation-tag-changed.yaml" "stable-sdk-operation-tags-changed"
expect_fail "stable operation tag change through path item ref" "$fixtures/sdk-base.yaml" "$fixtures/sdk-operation-tag-changed-via-pathitem-ref.yaml" "stable-sdk-operation-tags-changed"

set +e
version_output="$(OASDIFF_BIN=/usr/bin/true "$checker" "$fixtures/base.yaml" "$fixtures/base.yaml" 2>&1)"
version_status=$?
set -e
if [[ $version_status -eq 0 ]] || ! grep -Fq "must be github.com/oasdiff/oasdiff v1.23.0" <<<"$version_output"; then
  echo "unpinned OASDIFF_BIN was not rejected" >&2
  exit 1
fi

echo "oasdiff compatibility policy fixtures passed"
