#!/usr/bin/env node

import fs from "node:fs";

const agentFrameworkReadme = "examples/agent-framework-webhooks/README.md";
const requiredAgentFrameworkFiles = [
  "examples/agent-framework-webhooks/python/agent_webhooks/adapters/openai.py",
  "examples/agent-framework-webhooks/python/agent_webhooks/adapters/anthropic.py",
  "examples/agent-framework-webhooks/python/agent_webhooks/adapters/langchain.py",
  "examples/agent-framework-webhooks/python/agent_webhooks/adapters/adk.py",
  "examples/agent-framework-webhooks/typescript/src/adapters/openai.ts",
  "examples/agent-framework-webhooks/typescript/src/adapters/anthropic.ts",
  "examples/agent-framework-webhooks/typescript/src/adapters/langchain.ts",
  "examples/agent-framework-webhooks/typescript/src/adapters/adk.ts",
];

const requiredAgentFrameworkDocs = new Map([
  [agentFrameworkReadme, [
    /python -m agent_webhooks\.dry_run/,
    /npm run dry-run/,
    /client\.inbound\.from_event/,
    /client\.inbound\.fromEvent/,
  ]],
  ["README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
  ["sdks/python/README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
  ["sdks/typescript/README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
]);

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
for (const file of requiredAgentFrameworkFiles) {
  if (!fs.existsSync(file)) {
    console.error(`${file}: required agent-framework adapter is missing`);
    failed = true;
  }
}

for (const [file, patterns] of requiredAgentFrameworkDocs) {
  if (!fs.existsSync(file)) {
    console.error(`${file}: required agent-framework documentation is missing`);
    failed = true;
    continue;
  }
  const source = fs.readFileSync(file, "utf8");
  for (const pattern of patterns) {
    if (!pattern.test(source)) {
      console.error(`${file}: required agent-framework example contract missing ${pattern}`);
      failed = true;
    }
  }
}

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
