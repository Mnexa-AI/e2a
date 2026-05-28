#!/usr/bin/env node

import { login } from "../commands/login.js";
import {
  agentsList,
  agentsRegister,
  agentsDelete,
  agentsUpdate,
} from "../commands/agents.js";
import { inbox } from "../commands/inbox.js";
import { read } from "../commands/read.js";
import { forward } from "../commands/forward.js";
import { labels } from "../commands/labels.js";
import { reply } from "../commands/reply.js";
import { send } from "../commands/send.js";
import { config } from "../commands/config.js";
import { listen } from "../commands/listen.js";
import { domainsList, domainsRegister, domainsVerify, domainsDelete } from "../commands/domains.js";
import {
  pendingList,
  pendingShow,
  pendingApprove,
  pendingReject,
} from "../commands/pending.js";
import { createRequire } from "module";

const require = createRequire(import.meta.url);
const pkg = require("../../package.json") as { version: string };

const USAGE = `e2a — email for AI agents

Usage:
  e2a login                         Log in via browser and save config
  e2a agents list                   List your agents
  e2a agents register <slug>        Register an agent on the deployment's shared domain
  e2a agents update <email> ...     Update agent settings (HITL, webhook)
  e2a agents delete <email>         Delete an agent
  e2a pending list                  List messages held for human approval
  e2a pending show <id>             Show a held message's full detail
  e2a pending approve <id> [--edit] Approve (and optionally edit) a held message
  e2a pending reject <id> [--reason …]  Reject a held message
  e2a inbox [--unread|--read] [--limit N] [--oldest] [--from substr] [--subject substr] [--conversation id] [--since ts] [--until ts] [--token …]   List messages (newest first; --oldest for FIFO)
  e2a read <message-id>             Read a message
  e2a reply <msg-id> --body … [--reply-all] [--cc …] [--bcc …]
  e2a forward <msg-id> --to … [--cc …] [--bcc …] [--body …]
  e2a labels <msg-id> [--add <label> …] [--remove <label> …]
  e2a send [--to …] [--cc …] [--bcc …] --subject … --body …
  e2a listen [options]              Listen for emails via WebSocket
  e2a domains list                  List your domains
  e2a domains register <domain>     Register a custom domain
  e2a domains verify <domain>       Verify domain DNS records
  e2a domains delete <domain>       Delete a domain
  e2a config [list|get|set]         View or update config

Options:
  --help     Show this help
  --version  Show version
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
    case "agents": {
      const sub = args[0];
      if (sub === "list") {
        await agentsList(getFlag(args, "--agent"));
      } else if (sub === "register") {
        await agentsRegister(args[1], getFlag(args, "--name"));
      } else if (sub === "update") {
        // --hitl and --no-hitl are mutually exclusive; let --no-hitl win
        // if both are present (matches typical flag-parsing behavior).
        let hitlEnabled: boolean | undefined;
        if (hasFlag(args, "--no-hitl")) hitlEnabled = false;
        else if (hasFlag(args, "--hitl")) hitlEnabled = true;

        const ttlRaw = getFlag(args, "--hitl-ttl");
        let hitlTTLSeconds: number | undefined;
        if (ttlRaw !== undefined) {
          const n = parseInt(ttlRaw, 10);
          if (!Number.isFinite(n) || n <= 0) {
            process.stderr.write("--hitl-ttl must be a positive integer (seconds)\n");
            process.exit(1);
          }
          hitlTTLSeconds = n;
        }

        const actionRaw = getFlag(args, "--hitl-expiration-action");
        let hitlExpirationAction: "approve" | "reject" | undefined;
        if (actionRaw !== undefined) {
          if (actionRaw !== "approve" && actionRaw !== "reject") {
            process.stderr.write("--hitl-expiration-action must be 'approve' or 'reject'\n");
            process.exit(1);
          }
          hitlExpirationAction = actionRaw;
        }

        await agentsUpdate(args[1], {
          hitlEnabled,
          hitlTTLSeconds,
          hitlExpirationAction,
          webhookUrl: getFlag(args, "--webhook-url"),
        });
      } else if (sub === "delete") {
        await agentsDelete(args[1]);
      } else {
        process.stderr.write("Usage: e2a agents [list|register|update|delete]\n");
        process.exit(1);
      }
      break;
    }
    case "pending": {
      const sub = args[0];
      if (sub === "list") {
        await pendingList();
      } else if (sub === "show") {
        await pendingShow(args[1]);
      } else if (sub === "approve") {
        // Default the idempotency key to the message_id when the user
        // doesn't pass one. Approve fires SES, and a fresh per-call
        // UUIDv4 (the SDK fallback) provides zero protection across
        // a CLI retry loop — Ctrl-C, hit-up, run-again is the actual
        // user pattern. Tying the default to the message_id makes
        // every retry of the same approve replay the original
        // response. Override with --idempotency-key when the same
        // message needs a fresh attempt (e.g. after a relay outage).
        await pendingApprove(args[1], {
          edit: hasFlag(args, "--edit"),
          idempotencyKey: getFlag(args, "--idempotency-key") ?? args[1],
        });
      } else if (sub === "reject") {
        await pendingReject(args[1], getFlag(args, "--reason"));
      } else {
        process.stderr.write("Usage: e2a pending [list|show|approve|reject]\n");
        process.exit(1);
      }
      break;
    }
    case "inbox": {
      const status = hasFlag(args, "--unread")
        ? "unread"
        : hasFlag(args, "--read")
          ? "read"
          : "all";
      const limitStr = getFlag(args, "--limit");
      let limit = 20;
      if (limitStr) {
        limit = parseInt(limitStr, 10);
        if (!Number.isFinite(limit) || limit < 1) {
          process.stderr.write("--limit must be a positive integer\n");
          process.exit(1);
        }
      }
      const token = getFlag(args, "--token");
      // --oldest flips the inbox to FIFO order (oldest first). Useful
      // when draining a backlog with a poller that processes one
      // message at a time. Default is the new server-side default,
      // newest-first.
      const sort: "asc" | undefined = hasFlag(args, "--oldest") ? "asc" : undefined;
      await inbox(status, limit, token, getFlag(args, "--agent"), sort, {
        from: getFlag(args, "--from"),
        subjectContains: getFlag(args, "--subject"),
        conversationId: getFlag(args, "--conversation"),
        since: getFlag(args, "--since"),
        until: getFlag(args, "--until"),
      });
      break;
    }
    case "read":
      await read(args[0], getFlag(args, "--agent"));
      break;
    case "reply":
      await reply(args[0], getFlag(args, "--body") || "", {
        htmlBody: getFlag(args, "--html-body"),
        replyAll: hasFlag(args, "--reply-all"),
        cc: getFlags(args, "--cc"),
        bcc: getFlags(args, "--bcc"),
        from: getFlag(args, "--agent"),
        idempotencyKey: getFlag(args, "--idempotency-key"),
      });
      break;
    case "forward":
      await forward(args[0], {
        to: getFlags(args, "--to"),
        cc: getFlags(args, "--cc"),
        bcc: getFlags(args, "--bcc"),
        body: getFlag(args, "--body"),
        htmlBody: getFlag(args, "--html-body"),
        from: getFlag(args, "--agent"),
        idempotencyKey: getFlag(args, "--idempotency-key"),
      });
      break;
    case "labels":
      await labels(args[0], {
        add: getFlags(args, "--add"),
        remove: getFlags(args, "--remove"),
        from: getFlag(args, "--agent"),
      });
      break;
    case "send":
      await send(
        getFlags(args, "--to"),
        getFlag(args, "--subject") || "",
        getFlag(args, "--body") || "",
        {
          htmlBody: getFlag(args, "--html-body"),
          cc: getFlags(args, "--cc"),
          bcc: getFlags(args, "--bcc"),
          from: getFlag(args, "--agent"),
          idempotencyKey: getFlag(args, "--idempotency-key"),
        },
      );
      break;
    case "listen":
      await listen({
        agent: getFlag(args, "--agent"),
        json: hasFlag(args, "--json"),
        forward: getFlag(args, "--forward"),
        forwardToken: getFlag(args, "--forward-token"),
      });
      break;
    case "domains": {
      const sub = args[0];
      if (sub === "list") {
        await domainsList();
      } else if (sub === "register") {
        await domainsRegister(args[1]);
      } else if (sub === "verify") {
        await domainsVerify(args[1]);
      } else if (sub === "delete") {
        await domainsDelete(args[1]);
      } else {
        process.stderr.write("Usage: e2a domains [list|register|verify|delete]\n");
        process.exit(1);
      }
      break;
    }
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
    process.exit(1);
  });
}

export { getFlag, hasFlag, parseArgs };
