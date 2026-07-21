# e2a MCP bootstrap workflow

## Goal

Make the e2a skill capable of determining whether its MCP server is available,
helping a user connect it when necessary, verifying authentication, and leaving
the agent with a usable inbox before it attempts the user's original e2a task.

## Scope

This changes agent guidance and its regression checks. It does not change the
MCP server, OAuth implementation, plugin manifests, or e2a backend behavior.
The workflow remains focused on interactive MCP usage and does not introduce
human-in-the-loop setup guidance.

## Bootstrap workflow

Before the first e2a operation in a task, the agent checks whether e2a MCP tools
are present. The check is for the e2a tool surface, not a shell executable.

If the tools are present, the agent calls the e2a MCP `whoami` tool (commonly
exposed as `mcp__e2a__whoami`). It must not invoke the Unix `whoami` command.

If the tools are absent, the agent identifies the current client when possible
and gives the shortest applicable connection instructions:

- Claude Code: add `https://api.e2a.dev/mcp`, then use `/mcp` to authorize.
- Codex: configure the same remote MCP URL, then run `codex mcp login e2a`.
- Cursor or Windsurf: add an `mcpServers.e2a.url` entry for the hosted endpoint
  and complete the client's OAuth prompt.
- Other remote-MCP clients: configure the hosted Streamable HTTP endpoint and
  complete OAuth 2.1 authorization.

The skill includes these essentials inline and points to `https://e2a.dev/e2a.md`
for alternate clients and deeper troubleshooting. It never asks an interactive
user to paste an API key when OAuth is available, and it does not silently edit
the user's client configuration. After the user completes the interactive
step, the agent retries the MCP availability check and calls e2a `whoami`.

## Failure classification

The workflow distinguishes three cases rather than treating every failure as
an unconfigured client:

- Missing e2a tools: guide MCP configuration.
- Authentication failure from e2a `whoami`: guide reauthorization in the
  current client, then retry once the user completes it.
- Network, timeout, or server failure: report the operational error and avoid
  replacing known-good configuration or requesting new credentials.

## Establishing a usable inbox

When e2a `whoami` succeeds:

- An agent-scoped session uses the returned `agent_email`.
- An account-scoped session calls `list_agents`. If the task already identifies
  an inbox, the agent selects that inbox; otherwise it asks when multiple
  choices exist. If none exist, it offers to create a shared-domain address at
  `agents.e2a.dev`, which requires no DNS setup.

The agent verifies readiness with a harmless inbox read such as
`list_messages`, passing the selected email for account-scoped sessions. It
then resumes the user's original e2a request.

## Documentation shape

Replace the current ambiguous first-run paragraph with a compact decision
workflow. Keep client-specific commands short and retain the canonical setup
guide as the detailed reference. Tool names must explicitly say “e2a MCP tool”
where confusion with a shell command is plausible.

## Verification

Repository checks will assert that the skill:

- checks tool availability before calling e2a `whoami`;
- identifies `whoami` as an MCP tool and rejects the shell-command reading;
- contains setup branches for Claude Code, Codex, Cursor/Windsurf, and generic
  remote-MCP clients;
- distinguishes missing tools, authentication failures, and operational
  failures;
- verifies a selected inbox and resumes the original task;
- does not recommend API keys for interactive OAuth setup; and
- retains valid YAML frontmatter and passes the plugin validator.
