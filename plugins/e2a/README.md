# e2a plugin

Gives an AI coding agent a real, authenticated email inbox. Installing this
plugin registers the hosted **e2a MCP server** (`https://api.e2a.dev/mcp`,
Streamable HTTP + OAuth 2.1) and an **operate-well skill** so the agent can send
and receive email, reply in-thread, hold outbound mail for human review (HITL),
manage agents and custom domains, and work with attachments.

On first use of an MCP tool the agent runs the OAuth flow in your browser — no
API key to paste. (For headless/CI, an account API key works too; see
[`clients/`](./clients).)

## Install

The same plugin ships native manifests for Claude Code, Codex, and Cursor.

### Claude Code

```
claude plugin marketplace add Mnexa-AI/e2a
claude plugin install e2a@e2a
```

### Codex

```
codex plugin marketplace add Mnexa-AI/e2a
```

Then launch `codex`, run `/plugins`, search for **e2a**, and install — it walks
you through the OAuth path. (Codex desktop: **Plugins → Add more + →** paste
`https://github.com/Mnexa-AI/e2a`.)

### Cursor

In Cursor, run `/add-plugin e2a`, or paste `https://github.com/Mnexa-AI/e2a`
into the marketplace search in Cursor Settings and add it.

### Other MCP clients (manual)

Clients without native plugin support (Zed, Goose, Windsurf, Claude Desktop, raw
`mcp.json`) can point straight at the hosted server. Ready-to-paste configs are
in [`clients/`](./clients); the full per-client guide is at
<https://e2a.dev/e2a.md>.

## What's inside

```
plugins/e2a/
├── .claude-plugin/plugin.json   # Claude Code manifest
├── .codex-plugin/plugin.json    # Codex manifest (skills + mcpServers + interface)
├── .cursor-plugin/plugin.json   # Cursor manifest
├── .mcp.json                    # the hosted MCP server (single source of truth)
├── assets/icon.svg
├── skills/e2a/SKILL.md          # the "operate-well" skill (surfaces as /e2a)
└── clients/                     # manual paste-in configs for non-plugin clients
```

The marketplace manifests that expose this plugin live at the repo root:
`.claude-plugin/marketplace.json`, `.cursor-plugin/marketplace.json`, and
`.agents/plugins/marketplace.json` (Codex).

## Developing

The skill is authored in `skills/<name>/SKILL.md` with YAML frontmatter:

```markdown
---
name: e2a
description: Use when operating e2a over its MCP tools — sending/receiving email, ...
version: 11
---

...guide body...
```

- `name` (required) — must match the directory; lowercase letters, digits,
  hyphens; ≤64 chars.
- `description` (required) — write it as "Use when…"; this is how Claude Code
  decides to load the skill. ≤1024 chars.

`node scripts/validate-plugin.mjs` (run by the **Plugin manifests** CI job)
validates every manifest parses, that the version is identical across all
Claude/Codex/Cursor manifests, that marketplace `source` paths resolve, and that
each `SKILL.md` satisfies Claude Code's frontmatter constraints. A change that
wouldn't load fails CI.

When bumping the plugin version, update `.claude-plugin/plugin.json` (the source
of truth) **and** the other manifests + marketplace metadata to match — the
validator fails on drift.

## Reference

- Connect / clients / first inbox: <https://e2a.dev/e2a.md>
- Auth (OAuth 2.1 DCR + PKCE, API keys, scopes): <https://e2a.dev/auth.md>
- Webhook + SDK code: <https://e2a.dev/sdk.md>
- Docs index: <https://e2a.dev> (machine-readable: <https://e2a.dev/llms.txt>)
