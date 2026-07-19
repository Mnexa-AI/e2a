# Short Agent Prompts

## Goal

Make the dashboard's copyable coding-agent prompts quick to understand and
paste. The prompt should state the page-specific outcome and the hosted e2a MCP
endpoint without prescribing a long implementation sequence.

## Scope

Replace only the `prompt` text for the three existing `AgentPromptCard`
variants:

- Inboxes: `Help me set up an e2a inbox using https://api.e2a.dev/mcp`
- Domains: `Help me connect a custom domain to e2a using https://api.e2a.dev/mcp`
- Templates: `Help me set up e2a email templates using https://api.e2a.dev/mcp`

The shared heading, explanatory blurbs, copy interaction, card layout, and MCP
URL remain unchanged.

## Testing

Update the shared card tests to assert the three exact short prompts and their
common MCP endpoint. Preserve the clipboard regression test so it continues to
prove that the displayed prompt is copied verbatim.

## Deferred Work

This slice does not move the Domains card, add a Webhooks prompt card, relocate
agent documentation, or change the hosted Markdown routes. Those changes can be
handled separately after the prompt copy is shortened.
