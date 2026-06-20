#!/usr/bin/env node

// e2a CLI — slimmed to its non-duplicative surface (GA). Agent CRUD, messaging,
// domains, webhooks, events, and HITL all live in the MCP tools, the SDKs, and
// the web dashboard; the CLI keeps only what those don't cover ergonomically:
//   - login   : browser auth → ~/.e2a/config.json
//   - listen  : real-time inbound over WebSocket, with --forward to bridge a
//               local HTTP handler (the `stripe listen --forward-to` pattern)
//   - config  : view/update the local config
import { login } from "../commands/login.js";
import { config } from "../commands/config.js";
import { listen } from "../commands/listen.js";
import { createRequire } from "module";

const require = createRequire(import.meta.url);
const pkg = require("../../package.json") as { version: string };

const USAGE = `e2a — email for AI agents

The CLI is a thin developer convenience. Drive agents via the MCP tools or the
SDKs (@e2a/sdk, e2a); manage domains/agents/webhooks/keys in the dashboard.

Usage:
  e2a login                         Log in via browser and save config
  e2a listen [options]              Stream inbound email over WebSocket
        --agent <email>            Agent inbox to listen on (or config agent_email)
        --forward <url>            POST each message to a local URL (dev webhook proxy)
        --forward-token <token>    Bearer token to send with --forward requests
        --json                     Emit raw JSON notifications
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
    process.exit(1);
  });
}

export { getFlag, getFlags, hasFlag, parseArgs };
