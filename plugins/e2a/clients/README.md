# e2a MCP — client configs

Ready-to-paste config for connecting coding agents / editors to e2a's hosted MCP
server (`https://api.e2a.dev/mcp`, Streamable HTTP + OAuth 2.1):

- **`mcp.json`** — Cursor / Windsurf / Claude Desktop (any client using the
  `mcpServers` + `url` shape).
- **`vscode.mcp.json`** — VS Code / GitHub Copilot (`.vscode/mcp.json`; note the
  `servers` key + explicit `type`).
- **`codex.toml`** — OpenAI Codex CLI (`~/.codex/config.toml`; bridges to the
  hosted HTTP server via the `mcp-remote` stdio shim).

Clients that speak remote MCP take the URL directly and run OAuth in the browser;
stdio-only clients (Codex, Zed) wrap it with `npx -y mcp-remote …`.

**Full per-client guide** — Zed, Goose, headless API-key auth, and more:
https://e2a.dev/e2a.md (the "Connecting other MCP clients" section)

**Claude Code / Codex / Cursor** users don't need any of this — install the
plugin instead (it registers the MCP server via the plugin's `.mcp.json`). See
[`../README.md`](../README.md) for the one-command install per client.
