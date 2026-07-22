#!/usr/bin/env node
// Node-runtime helper for the ts_sdk interface (tests/sdk-monitor/monitor.py).
//
// The Python service shells out to this script rather than embedding a JS
// runtime. It imports `@e2a/sdk/v1` resolved from node_modules — populated at
// image build time by `npm install @e2a/sdk@5.2.0` (see ../Dockerfile), the
// same "published package only, never workspace source" discipline as
// requirements.txt for the Python SDK.
//
// Secrets travel via environment only (E2A_API_KEY), never argv — subject,
// body, and addresses are not secret and are passed as positional args.
//
// Usage:
//   node monitor-helper.mjs send <fromAgent> <toAgent> <subject> <body>
//   node monitor-helper.mjs reply <inboxAgent> <messageId> <text> <idempotencyKey>

import { E2AClient } from "@e2a/sdk/v1";

const [, , cmd, ...args] = process.argv;

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}

// Mirrors cli/src/commands/send.ts's emitSendResult: SendResultView.status is
// an open set (api/openapi.yaml), and only "sent"/"accepted" are genuinely
// successful outcomes. "pending_review" (held for review) and "failed" (or
// any status this build doesn't recognize) must not be reported as success —
// without this check the parent monitor.py (_run_subprocess) would see exit
// 0 and log a held/failed send as monitor_tick success, only surfacing the
// problem later as a monitor_stale timeout instead of an immediate
// monitor_error(stage="send"/"reply").
const SEND_OK_STATUSES = new Set(["sent", "accepted"]);

function checkSendStatus(result) {
  if (!SEND_OK_STATUSES.has(result?.status)) {
    // process.exitCode (not process.exit()) so the stdout write below still
    // flushes before the process exits — same reasoning as the CLI's
    // emitSendResult.
    process.stderr.write(`non-success send status: ${JSON.stringify(result?.status)}\n`);
    process.exitCode = 1;
  }
}

const apiKey = process.env.E2A_API_KEY;
if (!apiKey) fail("missing E2A_API_KEY");

// E2A_API_URL is the canonical env name the SDK reads (falls back to the
// hosted default if unset, matching the SDK's own resolution order).
const client = new E2AClient({ apiKey, baseUrl: process.env.E2A_API_URL });

async function main() {
  if (cmd === "send") {
    const [from, to, subject, body] = args;
    if (!from || !to || subject === undefined || body === undefined) {
      fail("usage: monitor-helper.mjs send <from> <to> <subject> <body>");
    }
    const result = await client.messages.send(from, { to: [to], subject, text: body });
    process.stdout.write(JSON.stringify(result) + "\n");
    checkSendStatus(result);
    return;
  }
  if (cmd === "reply") {
    const [inbox, messageId, text, idempotencyKey] = args;
    if (!inbox || !messageId || text === undefined) {
      fail("usage: monitor-helper.mjs reply <inbox> <message-id> <text> <idempotency-key>");
    }
    const opts = idempotencyKey ? { idempotencyKey } : {};
    const result = await client.messages.reply(inbox, messageId, { text }, opts);
    process.stdout.write(JSON.stringify(result) + "\n");
    checkSendStatus(result);
    return;
  }
  fail(`unknown command: ${cmd ?? "(none)"}`);
}

main().catch((err) => {
  fail(err?.message ?? String(err));
});
