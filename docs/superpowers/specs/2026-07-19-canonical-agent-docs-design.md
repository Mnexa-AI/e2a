# Canonical Agent Docs and Hosted Mirrors

## Problem Statement

The agent-facing Markdown at `web/public/e2a.md` and
`web/public/templates.md` is published at stable e2a.dev URLs, but its current
location makes the website tree appear authoritative. Agent documentation and
skills should instead be authored together in the OSS plugin tree while the
website continues to publish stable mirrors.

## Goals

- Make `plugins/e2a/docs/e2a.md` and `plugins/e2a/docs/templates.md` the
  canonical sources.
- Preserve `https://e2a.dev/e2a.md` and
  `https://e2a.dev/templates.md` with byte-identical files in `web/public/`.
- Refresh the hosted mirrors automatically before a web production build.
- Fail the existing repository-integrity CI job when a committed mirror is
  missing or differs from its canonical source.
- Review all agent-facing docs and skills after the move so references remain
  accurate and consistently distinguish canonical repository paths from
  stable hosted URLs.

## Non-goals

- Do not change the published URLs or Markdown content as part of the move.
- Do not redirect e2a.dev Markdown URLs to GitHub.
- Do not relocate `plugins/e2a/skills/**`.
- Do not add runtime fetching or a website-server dependency.
- Do not address the deferred Domains-card placement or Webhooks prompt card.

## Proposed Design

Add a dependency-free Node script at `scripts/sync-agent-docs.mjs` with two
modes:

- Default mode copies each canonical file to its matching `web/public/`
  destination. It creates the destination directory if needed and only writes
  when bytes differ.
- `--check` mode performs no writes. It reports each missing or stale mirror
  and exits nonzero if any mismatch exists.

The file mapping is explicit in the script rather than discovered by glob, so
adding a new public agent document is a deliberate contract change.

Add `prebuild` to `web/package.json` so `npm run build` invokes the default sync
before Next.js static export. Extend
`scripts/check-repository-text-integrity.sh` to invoke the script with
`--check`; the existing `repository-integrity` CI job will therefore enforce
mirror freshness without another workflow or dependency installation.

The mirrors remain committed. This keeps changes reviewable, ensures the
static site has deployable files even outside the npm lifecycle, and lets CI
detect contributors who edit a mirror instead of its canonical source.

## Failure Handling

- Missing canonical source: both modes fail with a clear path-specific error.
- Missing hosted mirror: sync mode creates it; check mode fails.
- Stale hosted mirror: sync mode replaces it atomically enough for a local
  build; check mode fails without modifying the checkout.
- Unknown command-line option: fail with usage information rather than
  silently choosing write mode.
- Multiple mismatches: check every mapping and report all failures in one run.

## Testing and Verification

Add a Node built-in test suite for the sync logic using a temporary directory.
Tests cover missing and stale mirrors in check mode, exact copying in sync
mode, successful verification after sync, missing canonical sources, and
unknown options. The script will expose testable functions while keeping its
CLI entry point dependency-free.

Run:

- the sync script in `--check` mode;
- the repository-integrity script;
- the sync-script unit tests;
- the full web Jest suite;
- the web production build;
- a repository diff check confirming canonical files and mirrors are
  byte-identical.

Finally, review `plugins/e2a/docs/**`, `plugins/e2a/skills/**`,
`plugins/e2a/README.md`, `plugins/e2a/clients/**`, `web/public/llms.txt`, and
dashboard onboarding references. Hosted URLs should remain in user-facing
instructions; repository references that describe source ownership should use
the canonical `plugins/e2a/docs/` paths.
