#!/usr/bin/env node

// e2a CLI — the developer convenience surface plus the *scripting* surface.
// Interactive management (domains, webhooks, HITL queues) lives in the MCP
// tools, SDKs, and dashboard. The CLI covers what those can't:
//   - login   : browser auth → ~/.e2a/config.json
//   - listen  : real-time inbound over WebSocket, with --forward to bridge a
//               local HTTP handler (the `stripe listen --forward-to` pattern)
//   - config  : view/update the local config
//   - whoami / send / reply / messages : stateless primitives for shell-script
//     harnesses (skills, hooks, CI) with a documented exit-code contract —
//     see EXIT in ../exit.ts. Held sends (pending_review) exit 3, because an
//     HTTP-successful send that reached nobody must be distinguishable without
//     parsing JSON.
import { login } from "../commands/login.js";
import { config } from "../commands/config.js";
import { listen } from "../commands/listen.js";
import { whoami } from "../commands/whoami.js";
import { send, reply } from "../commands/send.js";
import { messagesList, messagesGet } from "../commands/messages.js";
import { agentsList, agentsCreate, agentsGet } from "../commands/agents.js";
import { protectionGet, protectionSet } from "../commands/protection.js";
import { keysCreate, keysList, keysDelete } from "../commands/keys.js";
import { EXIT } from "../exit.js";
import { E2AError, E2AAuthError, E2APermissionError } from "@e2a/sdk/v1";
import { createRequire } from "module";

const require = createRequire(import.meta.url);
const pkg = require("../../package.json") as { version: string };

const USAGE = `e2a — email for AI agents

Scriptable primitives for agent harnesses (send/reply/messages/whoami, with a
stable exit-code contract) plus login and real-time listen. Interactive
management (domains, webhooks, review queues) lives in the MCP tools, the SDKs
(@e2a/sdk, e2a), and the dashboard.

Usage:
  e2a login                         Log in via browser (account-scoped key)
        --agent <inbox>            Exchange for a least-privilege agent-scoped key
                                   bound to <inbox> (bare slugs expand on the
                                   shared domain); revokes the account bootstrap key
        --with-key [<key>]         Headless: validate + save a key (from the arg,
                                   $E2A_API_KEY, or stdin) — no browser needed
  e2a whoami [--json]               Show key identity: user, scope, bound agent, plan
  e2a agents list                   List owned inboxes (account key)
  e2a agents create <email> [--name <n>]   Create an inbox (account key)
  e2a agents get <email>            Show one inbox
  e2a keys create [--agent <inbox>] [--name <n>]   Mint a key; --agent = bound,
                                   least-privilege (plaintext printed once)
  e2a keys list / delete <id>       Inventory / revoke keys (account key)
  e2a protection get <email>        Show the protection (screening/review) config
  e2a protection set <email>        Flip review posture, only the named knobs
        --outbound-review on|off   off = sends go out unheld (gate=flag, scan=off)
        --inbound-review on|off    off = inbound delivered unheld
  e2a send [options]                Send an email as the agent
        --to <email>               Recipient (repeatable)
        --subject <s>              Subject line
        --body <text>              Plain-text body (or --body-file <f>)
        --html-file <f>            HTML body; text fallback derived if no --body
        --attach <file>            Attach a file (repeatable)
        --conversation-id <id>     Thread id (alias: --conversation)
        --idempotency-key <k>      Stable key so a retried invocation can't double-send
        --agent <email>            Sending inbox (or config agent_email / E2A_AGENT_EMAIL)
        --json                     Print the full send result as JSON
  e2a reply <message-id> [options]  Reply in-thread (same body options as send)
  e2a messages list [options]       List messages, oldest first
        --direction <d>            inbound|outbound|all
        --since <ISO>              Messages created AT or after this timestamp
                                   (inclusive — dedup by message id when cursoring)
        --conversation <id>        Filter to one conversation (alias: --conversation-id)
        --read-status <s>          unread|read|all (default all — safe for poll loops)
        --limit <n>                Stop after n messages
        --agent <email>            Inbox to list (or config agent_email)
        --json                     NDJSON instead of TSV (id, from, created_at)
  e2a messages get <id> [--text]    Fetch one message; marks it read
        --text                     Print body text only (parsed reply-text preferred)
        --agent <email>            Inbox to read (or config agent_email)
  e2a listen [options]              Stream inbound email over WebSocket
        --agent <email>            Agent inbox to listen on (or config agent_email)
        --forward <url>            POST each message to a local URL (dev webhook proxy)
        --forward-token <token>    Bearer token to send with --forward requests
        --conversation <id>        Only messages in this conversation
        --once                     Exit 0 after the first (matching) message
        --until <ISO>              With --once: deadline; prints TIMEOUT, exits 6
        --text                     With --once: print the message body text only
        --json                     Emit raw JSON notifications
  e2a config [list|get|set]         View or update config

Options:
  --help     Show this help
  --version  Show version

Exit codes (stable scripting contract):
  0  success
  1  transient error (network / 5xx / rate limit) — retry may help
  2  usage error (bad flags or arguments)
  3  send accepted but HELD for review (pending_review) — not delivered
  4  bad credentials or wrong key scope
  5  permanent request error (not found / invalid / conflict) — do NOT retry
  6  bounded wait (listen --once --until) expired with no matching message
`;

function parseArgs(argv: string[]): { command: string; args: string[] } {
  const args = argv.slice(2);
  const command = args[0] || "";
  return { command, args: args.slice(1) };
}

function getFlag(args: string[], flag: string): string | undefined {
  const idx = args.indexOf(flag);
  if (idx === -1) return undefined;
  const value = args[idx + 1];
  // Don't consume another flag as a value
  if (value === undefined || value.startsWith("--")) return undefined;
  return value;
}

function getFlags(args: string[], flag: string): string[] {
  const values: string[] = [];
  for (let i = 0; i < args.length; i++) {
    if (args[i] === flag) {
      const value = args[i + 1];
      if (value !== undefined && !value.startsWith("--")) {
        values.push(value);
      }
    }
  }
  return values;
}

function hasFlag(args: string[], flag: string): boolean {
  return args.includes(flag);
}

// Flags that take no value. Everything else starting with "--" consumes the
// next token, which getPositionals must skip to find bare arguments like a
// message id.
const BOOLEAN_FLAGS = new Set(["--json", "--text", "--once", "--help", "--version"]);

function getPositionals(args: string[]): string[] {
  const positionals: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg.startsWith("--")) {
      if (!BOOLEAN_FLAGS.has(arg)) i++; // skip the flag's value
      continue;
    }
    positionals.push(arg);
  }
  return positionals;
}

/**
 * True when `flag` appears as an actual flag — not as the VALUE of a
 * preceding value-taking flag. Without this, `e2a send --body "--help"`
 * would print usage and exit 0 with nothing sent: a silent drop.
 */
function hasBareFlag(args: string[], flag: string): boolean {
  for (let i = 0; i < args.length; i++) {
    if (args[i] !== flag) continue;
    const prev = i > 0 ? args[i - 1] : undefined;
    const prevTakesValue = prev !== undefined && prev.startsWith("--") && !BOOLEAN_FLAGS.has(prev);
    if (!prevTakesValue) return true;
  }
  return false;
}

/**
 * getFlag, but a flag that is present with a missing or flag-like value is a
 * loud usage error instead of a silent "flag not given" — a dropped
 * --conversation-id or --limit must never quietly change what a send or list
 * does. (Values that legitimately start with "--" go via --body-file.)
 */
function getFlagChecked(args: string[], flag: string): string | undefined {
  const value = getFlag(args, flag);
  if (value === undefined && args.includes(flag)) {
    process.stderr.write(`${flag} requires a value\n`);
    process.exit(EXIT.USAGE);
  }
  // An empty string is never a meaningful flag value here, and treating it as
  // "flag not given" downstream silently DROPS filters — `--conversation ""`
  // used to widen a thread poll to the entire mailbox.
  if (value === "") {
    process.stderr.write(`${flag} requires a non-empty value\n`);
    process.exit(EXIT.USAGE);
  }
  return value;
}

function getFlagsChecked(args: string[], flag: string): string[] {
  const values = getFlags(args, flag);
  const occurrences = args.filter((a) => a === flag).length;
  if (values.length !== occurrences || values.some((v) => v === "")) {
    process.stderr.write(`${flag} requires a non-empty value\n`);
    process.exit(EXIT.USAGE);
  }
  return values;
}

/**
 * Reject flags a command doesn't know, loudly. Without this, a typo'd
 * `--conversation-id` on `messages list` silently widens the query to the
 * whole mailbox with exit 0 — the silent-corruption class the exit-code
 * contract exists to prevent. Also rejects `--flag=value` (unsupported form)
 * with a pointer at the space-separated syntax.
 */
function checkFlags(args: string[], allowed: string[]): void {
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (!arg.startsWith("--")) continue;
    if (arg.includes("=")) {
      process.stderr.write(`${arg}: use space-separated values (--flag value), not --flag=value\n`);
      process.exit(EXIT.USAGE);
    }
    if (!allowed.includes(arg)) {
      process.stderr.write(`unknown flag: ${arg} (see e2a --help)\n`);
      process.exit(EXIT.USAGE);
    }
    if (!BOOLEAN_FLAGS.has(arg)) i++; // skip the flag's value
  }
}

async function main() {
  const { command, args } = parseArgs(process.argv);

  if (
    command === "" ||
    command === "help" ||
    command === "--help" ||
    command === "-h" ||
    hasBareFlag(args, "--help")
  ) {
    process.stdout.write(USAGE);
    return;
  }

  if (command === "--version" || command === "-v" || hasBareFlag(args, "--version")) {
    process.stdout.write(`e2a ${pkg.version}\n`);
    return;
  }

  switch (command) {
    case "login":
      checkFlags(args, ["--agent", "--with-key"]);
      await login({
        agent: getFlagChecked(args, "--agent"),
        withKey: hasFlag(args, "--with-key"),
        // Unchecked on purpose: bare `--with-key` (no value) means "take the
        // key from $E2A_API_KEY or stdin".
        key: getFlag(args, "--with-key"),
      });
      break;
    case "agents": {
      const sub = args[0];
      const rest = args.slice(1);
      if (sub === "list") {
        checkFlags(rest, ["--json"]);
        await agentsList({ json: hasFlag(rest, "--json") });
      } else if (sub === "create") {
        checkFlags(rest, ["--name", "--json"]);
        await agentsCreate(getPositionals(rest)[0], {
          name: getFlagChecked(rest, "--name"),
          json: hasFlag(rest, "--json"),
        });
      } else if (sub === "get") {
        checkFlags(rest, ["--json"]);
        await agentsGet(getPositionals(rest)[0], { json: hasFlag(rest, "--json") });
      } else {
        process.stderr.write("Usage: e2a agents [list|create <email>|get <email>]\n");
        process.exit(EXIT.USAGE);
      }
      break;
    }
    case "keys": {
      const sub = args[0];
      const rest = args.slice(1);
      if (sub === "create") {
        checkFlags(rest, ["--name", "--agent", "--json"]);
        await keysCreate({
          name: getFlagChecked(rest, "--name"),
          agent: getFlagChecked(rest, "--agent"),
          json: hasFlag(rest, "--json"),
        });
      } else if (sub === "list") {
        checkFlags(rest, ["--json"]);
        await keysList({ json: hasFlag(rest, "--json") });
      } else if (sub === "delete") {
        checkFlags(rest, []);
        await keysDelete(getPositionals(rest)[0]);
      } else {
        process.stderr.write("Usage: e2a keys [create [--agent <inbox>]|list|delete <id>]\n");
        process.exit(EXIT.USAGE);
      }
      break;
    }
    case "protection": {
      const sub = args[0];
      const rest = args.slice(1);
      if (sub === "get") {
        checkFlags(rest, ["--json"]);
        await protectionGet(getPositionals(rest)[0], { json: hasFlag(rest, "--json") });
      } else if (sub === "set") {
        checkFlags(rest, ["--outbound-review", "--inbound-review", "--json"]);
        await protectionSet(getPositionals(rest)[0], {
          outboundReview: getFlagChecked(rest, "--outbound-review"),
          inboundReview: getFlagChecked(rest, "--inbound-review"),
          json: hasFlag(rest, "--json"),
        });
      } else {
        process.stderr.write(
          "Usage: e2a protection [get <email>|set <email> [--outbound-review on|off] [--inbound-review on|off]]\n",
        );
        process.exit(EXIT.USAGE);
      }
      break;
    }
    case "whoami":
      checkFlags(args, ["--json"]);
      await whoami({ json: hasFlag(args, "--json") });
      break;
    case "send":
      checkFlags(args, [
        "--to", "--subject", "--body", "--body-file", "--html-file", "--attach",
        "--conversation-id", "--conversation", "--agent", "--idempotency-key", "--json",
      ]);
      await send({
        to: getFlagsChecked(args, "--to"),
        attach: getFlagsChecked(args, "--attach"),
        subject: getFlagChecked(args, "--subject"),
        body: getFlagChecked(args, "--body"),
        bodyFile: getFlagChecked(args, "--body-file"),
        htmlFile: getFlagChecked(args, "--html-file"),
        // --conversation accepted as an alias so send and messages list can't
        // trip each other's spelling.
        conversationId:
          getFlagChecked(args, "--conversation-id") ?? getFlagChecked(args, "--conversation"),
        agent: getFlagChecked(args, "--agent"),
        idempotencyKey: getFlagChecked(args, "--idempotency-key"),
        json: hasFlag(args, "--json"),
      });
      break;
    case "reply":
      checkFlags(args, [
        "--body", "--body-file", "--html-file", "--attach", "--agent", "--idempotency-key", "--json",
      ]);
      await reply(getPositionals(args)[0], {
        attach: getFlagsChecked(args, "--attach"),
        body: getFlagChecked(args, "--body"),
        bodyFile: getFlagChecked(args, "--body-file"),
        htmlFile: getFlagChecked(args, "--html-file"),
        agent: getFlagChecked(args, "--agent"),
        idempotencyKey: getFlagChecked(args, "--idempotency-key"),
        json: hasFlag(args, "--json"),
      });
      break;
    case "messages": {
      const sub = args[0];
      const rest = args.slice(1);
      if (sub === "list") {
        checkFlags(rest, [
          "--direction", "--since", "--conversation", "--conversation-id",
          "--read-status", "--limit", "--agent", "--json",
        ]);
        await messagesList({
          agent: getFlagChecked(rest, "--agent"),
          direction: getFlagChecked(rest, "--direction"),
          since: getFlagChecked(rest, "--since"),
          conversation:
            getFlagChecked(rest, "--conversation") ?? getFlagChecked(rest, "--conversation-id"),
          readStatus: getFlagChecked(rest, "--read-status"),
          limit: getFlagChecked(rest, "--limit"),
          json: hasFlag(rest, "--json"),
        });
      } else if (sub === "get") {
        checkFlags(rest, ["--text", "--json", "--agent"]);
        await messagesGet(getPositionals(rest)[0], {
          agent: getFlagChecked(rest, "--agent"),
          text: hasFlag(rest, "--text"),
          json: hasFlag(rest, "--json"),
        });
      } else {
        process.stderr.write("Usage: e2a messages [list|get <id>]\n");
        process.exit(EXIT.USAGE);
      }
      break;
    }
    case "listen":
      checkFlags(args, [
        "--agent", "--forward", "--forward-token", "--json",
        "--conversation", "--conversation-id", "--once", "--until", "--text",
      ]);
      await listen({
        agent: getFlagChecked(args, "--agent"),
        json: hasFlag(args, "--json"),
        // Checked variants: `listen --forward` with a missing value used to
        // silently listen WITHOUT forwarding — the silent-drop class again.
        forward: getFlagChecked(args, "--forward"),
        forwardToken: getFlagChecked(args, "--forward-token"),
        conversation:
          getFlagChecked(args, "--conversation") ?? getFlagChecked(args, "--conversation-id"),
        once: hasFlag(args, "--once"),
        until: getFlagChecked(args, "--until"),
        text: hasFlag(args, "--text"),
      });
      break;
    case "config":
      await config(args);
      break;
    default:
      process.stderr.write(`Unknown command: ${command}\n`);
      process.stderr.write(USAGE);
      process.exit(EXIT.USAGE);
  }
}

// Only skip main when imported for tests (vitest sets VITEST_WORKER_ID)
const isTestImport = typeof process !== "undefined" && !!process.env.VITEST_WORKER_ID;

if (!isTestImport) {
  main().catch((err) => {
    // Print the API error code when present so scripts can grep it even
    // without branching on exit codes.
    const code = err instanceof E2AError && err.code ? ` [${err.code}]` : "";
    process.stderr.write(`Error: ${err.message}${code}\n`);
    // Contract mapping: AUTH (4) = fix your key; REQUEST (5) = permanent
    // request error (404/409/422 — the SDK marks these non-retryable), do NOT
    // retry the identical invocation; ERROR (1) = transient, retry may help.
    const isAuth = err instanceof E2AAuthError || err instanceof E2APermissionError;
    const isPermanent = !isAuth && err instanceof E2AError && !err.retryable;
    process.exit(isAuth ? EXIT.AUTH : isPermanent ? EXIT.REQUEST : EXIT.ERROR);
  });
}

export { getFlag, getFlags, hasFlag, parseArgs, getPositionals, hasBareFlag };
