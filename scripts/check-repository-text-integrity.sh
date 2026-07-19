#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

conflicts="$(git grep -n -E '^(<<<<<<< |=======|>>>>>>> )' -- . || true)"
if [[ -n "$conflicts" ]]; then
  echo "unresolved merge-conflict markers found:" >&2
  echo "$conflicts" >&2
  exit 1
fi

node -e '
  const fs = require("node:fs");
  for (const file of process.argv.slice(1)) {
    JSON.parse(fs.readFileSync(file, "utf8"));
  }
' package-lock.json web/package-lock.json

node scripts/sync-agent-docs.mjs --check
node scripts/check-sdk-example-contracts.mjs
python3 -m unittest discover -s examples/adk-cloud-webhook -p 'test_*.py'
python3 -m unittest discover -s mcp/examples -p 'test_*.py'

echo "repository text integrity checks passed"
