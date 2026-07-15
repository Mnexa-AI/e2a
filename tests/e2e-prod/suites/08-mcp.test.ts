import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup } from "../harness/cleanup.ts";
import { HttpMcpClient, callTool } from "../harness/mcp.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const apiClient = new ApiClient();
const SUITE = "08-mcp";

// Talk to the DEPLOYED streamable-HTTP /mcp server (the co-versioned
// mcp-server image behind Caddy) — the same surface that ships to prod —
// rather than spawning a locally-built stdio binary. Endpoint defaults to
// `${E2A_URL}/mcp`; E2A_MCP_URL overrides.
const mcp = new HttpMcpClient(apiClient.env.mcpUrl, apiClient.env.apiKey);

before(async () => {
  info(SUITE, "transport", `MCP over HTTP → ${apiClient.env.mcpUrl}`);
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

test("mcp: whoami returns the account identity (agent_email when agent-scoped)", async () => {
  const r = await callTool(mcp, "whoami");
  assert.equal(r.isError, undefined, `whoami isError: ${JSON.stringify(r)}`);
  const text = r.content?.find((c) => c.type === "text")?.text;
  assert.ok(text, "text content present");
  // whoami → AccountView: { user, scope, plan_code, ... } and, ONLY for an
  // agent-scoped credential, agent_email (the single agent that key IS). There
  // is no top-level `email`, and the tool never guesses a 'default' agent.
  const parsed = JSON.parse(text!) as { scope?: string; agent_email?: string; user?: unknown };
  assert.ok(parsed.user, "whoami returns the authenticated user");
  assert.ok(parsed.scope === "account" || parsed.scope === "agent", `valid scope, got ${parsed.scope}`);
  if (parsed.scope === "agent") {
    assert.equal(parsed.agent_email, apiClient.env.primaryAgentEmail, "agent-scoped whoami returns the pinned agent");
  } else {
    info(SUITE, "whoami-scope", "account-scoped credential — no agent_email to pin");
  }
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
    text: "should never reach SMTP",
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
  // list_messages is cursor-paginated with no page-size knob in its strict
  // schema (direction / read_status / sort / cursor / search filters only).
  // Call with a valid arg; the strict-schema test below verifies unknown
  // keys are rejected.
  const r = await callTool(mcp, "list_messages", { direction: "inbound" });
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
  // `limit` is the raw HTTP API name, NOT a valid MCP arg for list_messages
  // (whose strict schema exposes direction/read_status/sort/cursor/filters).
  // With strict schemas, the unknown key should be rejected at the MCP layer.
  let errored = false;
  let detail = "";
  try {
    const r = await callTool(mcp, "list_messages", { limit: 5 });
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
  // `sort` is a declared param constrained to the enum "asc" | "desc". A value
  // outside the enum must be rejected by the schema (a genuine type/enum error,
  // distinct from the unknown-key rejection above).
  let errored = false;
  let detail = "";
  try {
    const r = await callTool(mcp, "list_messages", { sort: "not-a-sort-order" });
    if (r.isError) {
      errored = true;
      detail = `isError: ${r.content?.find((c) => c.type === "text")?.text?.slice(0, 200)}`;
    }
  } catch (e) {
    errored = true;
    detail = `JSON-RPC error: ${(e as Error).message}`;
  }
  assert.ok(errored, "invalid `sort` enum value should be rejected by the schema");
  info(SUITE, "bad-type-rejected", detail);
});

test("mcp: server stays alive across multiple tool calls", async () => {
  // Quick health-check: run a few list_agents in sequence to confirm process stability.
  for (let i = 0; i < 3; i++) {
    const r = await callTool(mcp, "list_agents");
    assert.equal(r.isError, undefined, `iteration ${i} failed: ${JSON.stringify(r)}`);
  }
});
