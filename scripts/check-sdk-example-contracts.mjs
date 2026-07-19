#!/usr/bin/env node

import fs from "node:fs";

const checks = new Map([
  ["README.md", [
    /meta\["recipient"\]/,
    /notif\.recipient/,
    /notif\.message_id/,
    /\{"body": "Got it!"/,
  ]],
  ["sdks/typescript/README.md", [
    /^\+ .*\{ status: "unread"/m,
    /^\+ .*\.messageId/m,
    /^\+ .*\{ body: /m,
    /^for await .*\{ status: "unread"/m,
    /m\.messageId/,
    /htmlBody:/,
  ]],
  ["sdks/python/README.md", [
    /messages\.reply\([^\n]+\{"body":/,
    /"body": "Hi from my agent!"/,
    /"html_body":/,
  ]],
  ["web/public/sdk.md", [
    /n\.delivered_to/,
    /n\.message_id/,
  ]],
  ["docs/events.md", [
    /\.toArray\(\);/,
    /^for e in client\.events\.list/m,
    /redeliver\(e\.id, webhook_id=/,
  ]],
]);

let failed = false;
for (const [file, patterns] of checks) {
  const source = fs.readFileSync(file, "utf8");
  for (const pattern of patterns) {
    if (pattern.test(source)) {
      console.error(`${file}: stale SDK example contract matched ${pattern}`);
      failed = true;
    }
  }
}

if (failed) process.exit(1);
console.log("SDK example contract checks passed");
