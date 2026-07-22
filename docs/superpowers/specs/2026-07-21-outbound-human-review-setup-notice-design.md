# Optional outbound human-review setup notice

## Goal

Make the inbox setup page and the installed e2a agent guidance explain how to
configure an inbox so every outbound email requires human review, without
changing the default inbox-creation flow or automatically enabling the policy.

## Dashboard notice

The inbox variant of `AgentPromptCard` will show a compact optional notice below
the existing copy-paste prompt. The primary prompt and its **Copy prompt** action
remain unchanged.

The notice will tell the user that they can ask their coding agent to enable
review for every outbound email and provide this instruction verbatim:

> Configure this inbox so every outbound email requires human review.

The notice is inbox-specific. The templates and domains variants of the shared
card will not display it. The implementation should follow the existing Loft
tokens and typography rather than introducing a new design-system component.

## Agent guidance

The canonical `plugins/e2a/skills/e2a/SKILL.md` setup workflow will gain an
optional outbound-review subsection. When a user asks for every outbound email
to require review, the agent must call `update_protection` for the selected
inbox with:

```json
{
  "outbound_gate_policy": "allowlist",
  "outbound_gate_allowlist": [],
  "outbound_gate_action": "review",
  "holds_on_expiry": "reject"
}
```

The guidance will explain why all four fields matter:

- An empty allowlist makes every recipient a gate non-match.
- The `review` action holds every non-matching outbound message for a human.
- `reject` prevents an unreviewed message from being sent when its hold expires.
- `open` combined with `review` is not equivalent: `open` matches every
  recipient, so the recipient gate holds nothing.

This remains opt-in. Agents must not enable the policy unless the user requests
it. Existing `pending_review` handling remains unchanged: a held send is
accepted but not dispatched, and the agent must not retry it.

The canonical setup guide at `plugins/e2a/docs/setup.md` will also include the
same optional instruction so agents following hosted setup documentation receive
the guidance even when the plugin skill is unavailable. Its committed hosted
mirror will be refreshed using the repository's existing sync script.

## Testing and validation

- Extend `AgentPromptCard` tests to verify the inbox notice and instruction are
  rendered.
- Verify templates and domains do not receive the inbox-only notice.
- Keep the existing assertion that the primary inbox prompt is unchanged.
- Run the focused web test for `AgentPromptCard`.
- Run the agent-doc sync check and plugin manifest validator to catch mirrored
  documentation or skill metadata drift.

## Non-goals

- Automatically enabling human review during inbox creation.
- Changing the copied primary inbox-setup prompt.
- Adding a second policy card or a dashboard control that mutates protection
  settings directly.
- Changing protection-policy API behavior, MCP tool schemas, or review-queue
  behavior.
