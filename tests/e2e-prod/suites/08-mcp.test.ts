import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup } from "../harness/cleanup.ts";
import { StdioMcpClient, callTool } from "../harness/mcp.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const apiClient = new ApiClient();
const SUITE = "08-mcp";

const mcp = new StdioMcpClient();

before(async () => {
  await mcp.start("node", ["/Users/joshzhang/Desktop/e2a/mcp/dist/index.js"], {
    E2A_API_KEY: apiClient.env.apiKey,
    E2A_BASE_URL: apiClient.env.apiUrl,
    E2A_AGENT_EMAIL: apiClient.env.primaryAgentEmail,
  });
});

after(async () => {
  await mcp.stop();
  const r = await cleanup(apiClient);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/08-mcp.json`);
});

test("mcp: tools/list returns the expected tool surface", async () => {
  const r = await mcp.call<{ tools: Array<{ name: string; description?: string }> }>("tools/list");
  assert.ok(Array.isArray(r.tools), "tools is array");
  const names = r.tools.map((t) => t.name).sort();
  info(SUITE, "tool-surface", `MCP exposes ${names.length} tools: ${names.join(", ")}`);
  // Should at least have these — adjust if the server changes.
  const required = ["list_agents", "whoami"];
  for (const req of required) {
    assert.ok(names.includes(req), `expected tool "${req}" in surface, got ${names.join(",")}`);
  }
});

test("mcp: list_agents returns user's agents", async () => {
  const r = await callTool(mcp, "list_agents");
  assert.equal(r.isError, undefined, `list_agents reported isError: ${JSON.stringify(r)}`);
  assert.ok(r.content && r.content.length > 0, "tool returned content");
  // Tool wraps JSON-as-text per the MCP SDK convention.
  const text = r.content!.find((c) => c.type === "text")?.text;
  assert.ok(text, "text content present");
  const parsed = JSON.parse(text!) as { agents?: Array<{ email: string }> };
  assert.ok(Array.isArray(parsed.agents), "agents array present");
  assert.ok(parsed.agents!.some((a) => a.email === apiClient.env.primaryAgentEmail), "primary agent listed");
});

test("mcp: whoami returns the env-pinned default agent", async () => {
  const r = await callTool(mcp, "whoami");
  assert.equal(r.isError, undefined, `whoami isError: ${JSON.stringify(r)}`);
  const text = r.content?.find((c) => c.type === "text")?.text;
  assert.ok(text, "text content present");
  const parsed = JSON.parse(text!) as { email?: string };
  assert.equal(parsed.email, apiClient.env.primaryAgentEmail, "whoami returns the pinned agent");
});

test("mcp: unknown tool name produces an error result (isError or JSON-RPC error)", async () => {
  // Per the MCP spec, tool-level errors use `isError: true` in the result;
  // JSON-RPC errors are reserved for protocol-level failures. The @mcp/sdk
  // implements this by catching tool-not-found and wrapping it as isError,
  // so a well-behaved client must check both layers.
  let errored = false;
  let detail = "";
  try {
    const r = await callTool(mcp, "this_tool_definitely_does_not_exist");
    if (r.isError) {
      errored = true;
      detail = `isError result: ${r.content?.find((c) => c.type === "text")?.text?.slice(0, 200)}`;
    }
  } catch (e) {
    errored = true;
    detail = `JSON-RPC error: ${(e as Error).message}`;
  }
  assert.ok(errored, "unknown tool must produce an error at one of the two layers");
  info(SUITE, "unknown-tool-err", detail);
});

test("mcp: send_email with invalid recipient returns isError, never sends mail", async () => {
  // Check if send_email is exposed first.
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  const sendTool = list.tools.find((t) => t.name === "send_email" || t.name === "send");
  if (!sendTool) {
    info(SUITE, "send-tool-absent", "no send_email tool in MCP surface — skipping invalid-recipient test");
    return;
  }
  const r = await callTool(mcp, sendTool.name, {
    to: ["definitely not a valid email"],
    subject: "should fail validation",
    body: "should never reach SMTP",
  });
  if (r.isError) {
    info(SUITE, "send-bad-recipient-error", "MCP send tool reported isError on invalid recipient — good");
  } else {
    const text = r.content?.find((c) => c.type === "text")?.text;
    fail(SUITE, "send-bad-recipient-accepted", `MCP send accepted invalid recipient: ${text?.slice(0, 200)}`);
  }
});

test("mcp: list_messages tool works against the inbox", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  const t = list.tools.find((x) => x.name === "list_messages");
  if (!t) {
    info(SUITE, "list-messages-absent", "no list_messages tool — skipping");
    return;
  }
  // page_size is the actual MCP tool param (not `limit`, which is the
  // raw HTTP API name). Use page_size here; the strict-schema test
  // below specifically verifies unknown keys like `limit` are rejected.
  const r = await callTool(mcp, "list_messages", { page_size: 5 });
  assert.equal(r.isError, undefined, `list_messages isError: ${JSON.stringify(r)}`);
  const text = r.content?.find((c) => c.type === "text")?.text;
  assert.ok(text, "text content present");
  const parsed = JSON.parse(text!) as { messages?: unknown[] };
  assert.ok(Array.isArray(parsed.messages), `messages array present`);
});

test("mcp: tool arg validation — unknown keys are rejected (strict schemas)", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  const t = list.tools.find((x) => x.name === "list_messages");
  if (!t) {
    info(SUITE, "list-messages-absent", "skipping arg-type test");
    return;
  }
  // `limit` is not a valid arg for list_messages (the MCP tool uses `page_size`).
  // With strict schemas, the unknown key should be rejected at the MCP layer.
  let errored = false;
  let detail = "";
  try {
    const r = await callTool(mcp, "list_messages", { limit: "not-a-number" });
    if (r.isError) {
      errored = true;
      detail = `isError: ${r.content?.find((c) => c.type === "text")?.text?.slice(0, 200)}`;
    }
  } catch (e) {
    errored = true;
    detail = `JSON-RPC error: ${(e as Error).message}`;
  }
  assert.ok(errored, "unknown arg `limit` should be rejected by strict schema");
  info(SUITE, "unknown-arg-rejected", detail);
});

test("mcp: tool arg validation — wrong type for declared param is rejected", async () => {
  // page_size is declared as z.number().int().positive(); a string must be rejected.
  let errored = false;
  let detail = "";
  try {
    const r = await callTool(mcp, "list_messages", { page_size: "not-a-number" });
    if (r.isError) {
      errored = true;
      detail = `isError: ${r.content?.find((c) => c.type === "text")?.text?.slice(0, 200)}`;
    }
  } catch (e) {
    errored = true;
    detail = `JSON-RPC error: ${(e as Error).message}`;
  }
  assert.ok(errored, "string page_size should be rejected by zod number validator");
  info(SUITE, "bad-type-rejected", detail);
});

test("mcp: server stays alive across multiple tool calls", async () => {
  // Quick health-check: run a few list_agents in sequence to confirm process stability.
  for (let i = 0; i < 3; i++) {
    const r = await callTool(mcp, "list_agents");
    assert.equal(r.isError, undefined, `iteration ${i} failed: ${JSON.stringify(r)}`);
  }
});
