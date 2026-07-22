#!/usr/bin/env node

import fs from "node:fs";

const agentFrameworkReadme = "examples/agent-framework-webhooks/README.md";
const requiredAgentFrameworkFiles = [
  "examples/agent-framework-webhooks/python/agent_webhooks/agent.py",
  "examples/agent-framework-webhooks/typescript/src/agent.ts",
];

const requiredAgentFrameworkDocs = new Map([
  [agentFrameworkReadme, [
    /python -m agent_webhooks\.dry_run/,
    /npm run dry-run/,
    /client\.inbound\.from_event/,
    /client\.inbound\.fromEvent/,
    /^## Anthropic$/m,
    /^## LangChain$/m,
    /^## Google ADK$/m,
  ]],
  ["README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
  ["sdks/python/README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
  ["sdks/typescript/README.md", [/examples\/agent-framework-webhooks\/README\.md/]],
]);

const requiredAgentFrameworkSource = new Map([
  ["examples/agent-framework-webhooks/python/agent_webhooks/handler.py", [
    /inbound\.from_event\(event\)/,
    /await email\.reply\(/,
  ]],
  ["examples/agent-framework-webhooks/typescript/src/handler.ts", [
    /inbound\.fromEvent\(event\)/,
    /await email\.reply\(/,
  ]],
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
    console.error(`${file}: required minimal OpenAI agent is missing`);
    failed = true;
  }
}

const executableAgentFrameworkSource = [
  ...fs.readdirSync("examples/agent-framework-webhooks/python/agent_webhooks", {
    recursive: true,
  }).filter((file) => file.endsWith(".py"))
    .map((file) => `examples/agent-framework-webhooks/python/agent_webhooks/${file}`),
  ...fs.readdirSync("examples/agent-framework-webhooks/typescript/src", {
    recursive: true,
  }).filter((file) => file.endsWith(".ts"))
    .map((file) => `examples/agent-framework-webhooks/typescript/src/${file}`),
];

const forbiddenLowLevelInboundCalls = [
  /client\.(?:api\.)?messages\.(?:get|get_message|getMessage|reply)\s*\(/,
];

for (const file of executableAgentFrameworkSource) {
  if (!fs.existsSync(file)) continue;
  const source = fs.readFileSync(file, "utf8");
  for (const pattern of forbiddenLowLevelInboundCalls) {
    if (pattern.test(source)) {
      console.error(`${file}: legacy low-level inbound call matched ${pattern}`);
      failed = true;
    }
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

for (const [file, patterns] of requiredAgentFrameworkSource) {
  if (!fs.existsSync(file)) {
    console.error(`${file}: required agent-framework handler is missing`);
    failed = true;
    continue;
  }
  const source = fs.readFileSync(file, "utf8");
  for (const pattern of patterns) {
    if (!pattern.test(source)) {
      console.error(`${file}: required ergonomic inbound operation missing ${pattern}`);
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
