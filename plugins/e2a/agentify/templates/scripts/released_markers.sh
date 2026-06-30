#!/usr/bin/env bash
# released_markers.sh — extract the issue numbers a merged push shipped.
#
# Reads a GitHub "pulls for a commit" JSON array on stdin (from
# `gh api repos/{repo}/commits/{sha}/pulls`) and prints the issue number of
# each MERGED, BOT-AUTHORED PR carrying a `fix:#<n>` marker in its body.
#
# Marker trust (design §5.5): the marker is honored ONLY from a PR authored
# by the bot ($AUTOREPO_BOT_LOGIN), footer form `<!-- {marker} fix:#N -->`.
# User feedback is quoted only into ISSUES, never PR descriptions, so a
# PR-body marker cannot be attacker-forged through intake — and a human/
# contributor pasting a marker into their OWN PR is ignored (wrong author).
#
# Env: AUTOREPO_BOT_LOGIN (required), AUTOREPO_MARKER (required).
# Usage: gh api .../commits/<sha>/pulls | released_markers.sh
#        released_markers.sh _selftest
set -euo pipefail

# _extract: PR-array JSON on stdin -> issue numbers (one per line).
_extract() {
  local bot="$1" marker="$2"
  jq -r --arg bot "$bot" '.[] | select(.user.login == $bot) | select(.merged_at != null) | .body' \
    | grep -oE "<!-- ${marker} fix:#[0-9]+ -->" \
    | grep -oE '[0-9]+'
}

if [ "${1:-}" = "_selftest" ]; then
  fail=0
  fix='<!-- acme-feedback fix:#42 -->'
  arr="$(jq -n --arg f "body text\n$fix" --arg g "body\n<!-- acme-feedback fix:#99 -->" '[
    {number:1, user:{login:"bot[bot]"}, merged_at:"2026-01-01T00:00:00Z", body:$f},
    {number:2, user:{login:"attacker"},  merged_at:"2026-01-01T00:00:00Z", body:$g},
    {number:3, user:{login:"bot[bot]"}, merged_at:null,                   body:$g},
    {number:4, user:{login:"bot[bot]"}, merged_at:"2026-01-01T00:00:00Z", body:"no marker here"}]')"
  out="$(printf '%s' "$arr" | _extract "bot[bot]" "acme-feedback" | tr '\n' ',')"
  [ "$out" = "42," ] || { echo "FAIL: expected '42,' got '$out' (must ignore non-bot #99, unmerged, and no-marker)"; fail=1; }
  if [ "$fail" = 0 ]; then echo "released_markers.sh selftest: OK"; else echo "released_markers.sh selftest: FAILED"; exit 1; fi
  exit 0
fi

: "${AUTOREPO_BOT_LOGIN:?required}"; : "${AUTOREPO_MARKER:?required}"
_extract "$AUTOREPO_BOT_LOGIN" "$AUTOREPO_MARKER"
