# clients.md

How to connect any MCP-capable coding agent / editor to e2a's hosted MCP
server. There's one endpoint:

```
https://api.e2a.dev/mcp        (Streamable HTTP, OAuth 2.1)
```

Two connection shapes:

- **Native remote MCP** — clients that speak Streamable HTTP MCP take the URL
  directly and run the OAuth browser consent on first use. No API key.
- **stdio bridge** — clients that only speak stdio MCP wrap the URL with
  [`mcp-remote`](https://www.npmjs.com/package/mcp-remote)
  (`npx -y mcp-remote https://api.e2a.dev/mcp`), which does the OAuth flow and
  bridges stdio ↔ HTTP.

Ready-to-paste config files for each client live in
[plugins/e2a/clients/](https://github.com/Mnexa-AI/e2a/tree/main/plugins/e2a/clients).

## Claude Code

The e2a plugin bundles everything (skill + MCP). Or add the server directly:

```
claude mcp add --transport http e2a https://api.e2a.dev/mcp
```

## Claude Desktop / Cursor / Windsurf

Native remote MCP — add to the host's MCP config (`claude_desktop_config.json`,
`~/.cursor/mcp.json`, Windsurf's `mcp_config.json`):

```json
{ "mcpServers": { "e2a": { "url": "https://api.e2a.dev/mcp" } } }
```

## VS Code (GitHub Copilot)

`.vscode/mcp.json` (note the `servers` key + explicit `type`):

```json
{ "servers": { "e2a": { "type": "http", "url": "https://api.e2a.dev/mcp" } } }
```

## OpenAI Codex CLI

Codex speaks stdio MCP, so bridge with `mcp-remote`. Add to `~/.codex/config.toml`:

```toml
[mcp_servers.e2a]
command = "npx"
args = ["-y", "mcp-remote", "https://api.e2a.dev/mcp"]
```

## Zed

`settings.json` → `context_servers`:

```json
{ "context_servers": { "e2a": { "source": "custom", "command": "npx",
  "args": ["-y", "mcp-remote", "https://api.e2a.dev/mcp"] } } }
```

## Goose

```bash
goose session --with-remote-extension https://api.e2a.dev/mcp
```

(or add it as an extension in `~/.config/goose/config.yaml`).

## Headless / CI (no browser for OAuth)

When a browser consent flow isn't available, authenticate with an account API
key via a header instead of OAuth — pass it to `mcp-remote`:

```bash
npx -y mcp-remote https://api.e2a.dev/mcp --header "Authorization: Bearer $E2A_API_KEY"
```

Mint the key in the dashboard (Settings → API keys). See
https://e2a.dev/auth.md for the full auth model.

---

Whatever the client, once connected the tool surface is identical — call
`tools/list` for the current signatures, and see https://e2a.dev/e2a.md for the
mental model.
