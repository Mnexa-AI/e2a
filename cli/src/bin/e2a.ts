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
import { EXIT } from "../exit.js";
import { E2AAuthError, E2APermissionError } from "@e2a/sdk/v1";
import { createRequire } from "module";

const require = createRequire(import.meta.url);
const pkg = require("../../package.json") as { version: string };

const USAGE = `e2a — email for AI agents

Scriptable primitives for agent harnesses (send/reply/messages/whoami, with a
stable exit-code contract) plus login and real-time listen. Interactive
management (domains, webhooks, review queues) lives in the MCP tools, the SDKs
(@e2a/sdk, e2a), and the dashboard.

Usage:
  e2a login                         Log in via browser and save config
  e2a whoami [--json]               Show key identity: user, scope, bound agent, plan
  e2a send [options]                Send an email as the agent
        --to <email>               Recipient (repeatable)
        --subject <s>              Subject line
        --body <text>              Plain-text body (or --body-file <f>)
        --html-file <f>            HTML body; text fallback derived if no --body
        --conversation-id <id>     Thread id for the agent's own threading
        --agent <email>            Sending inbox (or config agent_email)
        --json                     Print the full send result as JSON
  e2a reply <message-id> [options]  Reply in-thread (same body options as send)
  e2a messages list [options]       List messages, oldest first
        --direction <d>            inbound|outbound|all
        --since <ISO>              Only messages after this timestamp
        --conversation <id>        Filter to one conversation
        --limit <n>                Stop after n messages
        --json                     NDJSON instead of TSV (id, from, created_at)
  e2a messages get <id> [--text]    Fetch one message (--text = body text only)
  e2a listen [options]              Stream inbound email over WebSocket
        --agent <email>            Agent inbox to listen on (or config agent_email)
        --forward <url>            POST each message to a local URL (dev webhook proxy)
        --forward-token <token>    Bearer token to send with --forward requests
        --json                     Emit raw JSON notifications
  e2a config [list|get|set]         View or update config

Options:
  --help     Show this help
  --version  Show version

Exit codes (stable scripting contract):
  0  success
  1  network / server / unexpected error
  2  usage error (bad flags or arguments)
  3  send accepted but HELD for review (pending_review) — not delivered
  4  bad credentials or wrong key scope
  5  bounded wait hit its deadline
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
const BOOLEAN_FLAGS = new Set(["--json", "--text", "--help", "--version"]);

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

async function main() {
  const { command, args } = parseArgs(process.argv);

  if (
    command === "" ||
    command === "help" ||
    command === "--help" ||
    command === "-h" ||
    hasFlag(args, "--help")
  ) {
    process.stdout.write(USAGE);
    return;
  }

  if (command === "--version" || command === "-v" || hasFlag(args, "--version")) {
    process.stdout.write(`e2a ${pkg.version}\n`);
    return;
  }

  switch (command) {
    case "login":
      await login();
      break;
    case "whoami":
      await whoami({ json: hasFlag(args, "--json") });
      break;
    case "send":
      await send({
        to: getFlags(args, "--to"),
        subject: getFlag(args, "--subject"),
        body: getFlag(args, "--body"),
        bodyFile: getFlag(args, "--body-file"),
        htmlFile: getFlag(args, "--html-file"),
        conversationId: getFlag(args, "--conversation-id"),
        agent: getFlag(args, "--agent"),
        json: hasFlag(args, "--json"),
      });
      break;
    case "reply":
      await reply(getPositionals(args)[0], {
        body: getFlag(args, "--body"),
        bodyFile: getFlag(args, "--body-file"),
        htmlFile: getFlag(args, "--html-file"),
        agent: getFlag(args, "--agent"),
        json: hasFlag(args, "--json"),
      });
      break;
    case "messages": {
      const sub = args[0];
      const rest = args.slice(1);
      if (sub === "list") {
        await messagesList({
          agent: getFlag(rest, "--agent"),
          direction: getFlag(rest, "--direction"),
          since: getFlag(rest, "--since"),
          conversation: getFlag(rest, "--conversation"),
          limit: getFlag(rest, "--limit"),
          json: hasFlag(rest, "--json"),
        });
      } else if (sub === "get") {
        await messagesGet(getPositionals(rest)[0], {
          agent: getFlag(rest, "--agent"),
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
      await listen({
        agent: getFlag(args, "--agent"),
        json: hasFlag(args, "--json"),
        forward: getFlag(args, "--forward"),
        forwardToken: getFlag(args, "--forward-token"),
      });
      break;
    case "config":
      await config(args);
      break;
    default:
      process.stderr.write(`Unknown command: ${command}\n`);
      process.stderr.write(USAGE);
      process.exit(1);
  }
}

// Only skip main when imported for tests (vitest sets VITEST_WORKER_ID)
const isTestImport = typeof process !== "undefined" && !!process.env.VITEST_WORKER_ID;

if (!isTestImport) {
  main().catch((err) => {
    process.stderr.write(`Error: ${err.message}\n`);
    // Auth failures get their own exit code so scripts can distinguish "fix
    // your key" from a transient error worth retrying.
    const isAuth = err instanceof E2AAuthError || err instanceof E2APermissionError;
    process.exit(isAuth ? EXIT.AUTH : EXIT.ERROR);
  });
}

export { getFlag, getFlags, hasFlag, parseArgs, getPositionals };
