# e2a CLI

Command-line tool for [e2a](https://e2a.dev) — register agents, listen for emails in real time, and manage your inbox from the terminal.

## Install

```bash
npm install -g @e2a/cli
```

Or run without installing:

```bash
npx @e2a/cli login
```

## Setup

```bash
# Open browser login and save your API key + default agent automatically
e2a login

# Register a local-mode agent
e2a agents register my-bot
# → Registered: my-bot@agents.e2a.dev
```

## Commands

### `e2a listen`

Listen for emails via WebSocket in real time. This is the primary way local-mode agents receive emails.

```bash
# Human-readable output
e2a listen

# Output: [10:30:15] From: alice@example.com | Subject: Meeting tomorrow
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--agent <email>` | Agent email to listen as (defaults to config) |
| `--json` | Output full message JSON per line, for piping to other tools |
| `--forward <url>` | Fetch full message and POST to a local URL |
| `--forward-token <token>` | Auth token sent with `--forward` requests |

**JSON mode** (for piping):

```bash
e2a listen --json | jq '.subject'
```

**Forward mode** (proxy to local service):

```bash
e2a listen --forward http://localhost:3000/webhook --forward-token my_secret
```

The full message is fetched via REST and POSTed as JSON. The token is sent as `Authorization: Bearer`.

**OpenClaw integration:**

```bash
e2a listen --forward http://localhost:18789/v1/responses --forward-token <gateway-token>
```

Find your gateway token in `~/.openclaw/openclaw.json` under `gateway.auth.token`. The port defaults to `18789`.

When the forward URL ends in `/v1/responses`, the CLI:
1. Formats the email as an OpenAI Responses API payload and POSTs it with `Authorization: Bearer`
2. Parses the response and automatically sends it back as a reply email to the original sender

This works with any OpenAI Responses API-compatible endpoint, including OpenClaw.

The CLI reconnects automatically with exponential backoff (1s, 2s, 4s, …max 30s). Backoff resets after a successful connection. Press Ctrl+C to disconnect.

### `e2a agents list`

List all your registered agents. The active agent (from config) is marked.

```bash
e2a agents list
# → my-bot@agents.e2a.dev  local (active)
# → other@agents.e2a.dev   cloud
```

### `e2a agents register <slug>`

Register a new agent on `agents.e2a.dev`. Creates a local-mode agent (no public URL needed).

```bash
e2a agents register my-bot
# → Registered: my-bot@agents.e2a.dev
# → Agent email saved to ~/.e2a/config.json
```

### `e2a agents delete <email>`

Delete an agent.

```bash
e2a agents delete my-bot@agents.e2a.dev
# → Deleted: my-bot@agents.e2a.dev
```

### `e2a inbox`

List messages for your agent.

```bash
e2a inbox                    # All messages
e2a inbox --unread           # Unread only
e2a inbox --read             # Read only
e2a inbox --limit 5          # Limit results
e2a inbox --agent bot@agents.e2a.dev  # Specific agent
e2a inbox --token <token>    # Paginate (next page)
```

### `e2a read <message-id>`

Read a single message (marks it as read).

```bash
e2a read msg_abc123
e2a read msg_abc123 --agent bot@agents.e2a.dev  # Specific agent
```

### `e2a reply <message-id> --body <text>`

Reply to a message.

```bash
e2a reply msg_abc123 --body "Thanks for your email!"
e2a reply msg_abc123 --body "..." --agent bot@agents.e2a.dev
```

### `e2a send`

Send a new email.

```bash
e2a send --to alice@example.com --subject "Hello" --body "Hi Alice!"
e2a send --to alice@example.com --subject "Hello" --body "..." --agent bot@agents.e2a.dev
```

### `e2a forward <message-id>`

Forward an inbound message to another address, preserving the original content.

```bash
e2a forward msg_abc123 --to alice@example.com
e2a forward msg_abc123 --to alice@example.com --cc bob@example.com --body "FYI"
```

### `e2a labels <message-id>`

Add or remove labels on a message.

```bash
e2a labels msg_abc123 --add important --add follow-up
e2a labels msg_abc123 --remove follow-up
```

### `e2a pending`

Manage outbound mail held for human approval (HITL).

```bash
e2a pending list                      # List pending messages
e2a pending show <message-id>         # Show a held draft in full
e2a pending approve <message-id>      # Send a held message (use --edit to revise first)
e2a pending reject <message-id> --reason "..."  # Discard a held message
```

### `e2a conversations`

List and inspect conversation threads.

```bash
e2a conversations list                # List conversations
e2a conversations show <conversation-id>  # Show messages in a thread
```

### `e2a events`

Inspect and redeliver webhook events.

```bash
e2a events list                       # List recent events
e2a events get <event-id>             # Show one event
e2a events redeliver <event-id>       # Re-send a webhook event
```

### `e2a webhooks`

Manage webhook subscriptions.

```bash
e2a webhooks list                     # List webhook subscriptions
e2a webhooks create --url <url> --events <event> [--events <event> ...]
e2a webhooks get <id>                 # Show one subscription
e2a webhooks update <id> [--url ...] [--events ...] [--enable|--disable]
e2a webhooks delete <id>              # Remove a subscription
e2a webhooks rotate-secret <id>       # Rotate the signing secret
e2a webhooks test <id> [--event <event>]  # Send a test delivery
e2a webhooks deliveries <id> [--limit N] [--status pending|delivered|failed]
```

### `e2a domains list`

List your custom domains and their verification status.

```bash
e2a domains list
# → mycompany.com  verified
# → staging.dev    unverified
```

### `e2a domains register <domain>`

Register a custom domain. Prints the DNS records you need to add.

```bash
e2a domains register mycompany.com
# → Registered: mycompany.com
# → Add these DNS records to verify ownership:
# →   MX  mycompany.com  mx.e2a.dev  (priority 10)
# →   TXT mycompany.com  e2a-verify=abc123
# → Then run: e2a domains verify mycompany.com
```

### `e2a domains verify <domain>`

Check DNS records and verify ownership of a registered domain.

```bash
e2a domains verify mycompany.com
# → Verified: mycompany.com
```

### `e2a domains delete <domain>`

Remove a custom domain.

```bash
e2a domains delete mycompany.com
# → Deleted: mycompany.com
```

### `e2a config`

View or update CLI configuration.

```bash
e2a config list              # Show all config
e2a config get api_key       # Get a specific value
e2a config set api_url http://localhost:8080  # Override API URL
```

## Configuration

Config is stored in `~/.e2a/config.json` as JSON. Environment variables override file values:

| Variable | Description |
|----------|-------------|
| `E2A_API_KEY` | API key (overrides file) |
| `E2A_URL` | API base URL (overrides file, default: `https://e2a.dev`) |

## How it works

The `listen` command connects to e2a's WebSocket endpoint:

```
wss://e2a.dev/v1/agents/{email}/ws?token={api_key}
```

The server sends lightweight JSON notifications (message_id, from, subject, received_at) when emails arrive. The CLI fetches full message content via the REST API as needed (`--json` and `--forward` modes). This keeps the WebSocket connection fast and avoids large attachments blocking notifications.

When the CLI disconnects and reconnects, all messages that arrived while offline are drained immediately as notifications.

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.
