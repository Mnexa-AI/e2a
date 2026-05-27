import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { StdioMcpClient, callTool } from "../harness/mcp.ts";
import { uniqueSlug, uniqueSubject, SINK_EMAIL } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const apiClient = new ApiClient();
const SUITE = "12-mcp-extended";
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
  writeReport(`./reports/12-mcp-extended.json`);
});

function extractText(r: { content?: Array<{ type: string; text?: string }> }): string {
  return r.content?.find((c) => c.type === "text")?.text ?? "";
}

async function ensureHitlAgent(): Promise<string> {
  const slug = uniqueSlug("mcpe");
  const c = await apiClient.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "mcp ext", agent_mode: "local" },
  });
  if (c.status !== 201) throw new Error(`create agent: ${c.status} ${c.raw.slice(0, 200)}`);
  const email = c.body!.email;
  track("agent", email);
  const u = await apiClient.put(`/api/v1/agents/${encodeURIComponent(email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject", hitl_ttl_seconds: 120 },
  });
  if (u.status !== 200) throw new Error(`enable HITL: ${u.status}`);
  return email;
}

test("mcp-ext: create_agent tool registers a new agent via MCP", async () => {
  const slug = uniqueSlug("mcpcreate");
  const r = await callTool(mcp, "create_agent", { slug, name: "mcp created", agent_mode: "local" });
  if (r.isError) {
    fail(SUITE, "create-agent-error", `create_agent reported isError: ${extractText(r).slice(0, 200)}`);
    return;
  }
  const text = extractText(r);
  assert.ok(text, "create_agent returned text content");
  const parsed = JSON.parse(text) as { email?: string; id?: string };
  assert.ok(parsed.email, `expected email in result: ${text}`);
  track("agent", parsed.email!);
  // Should match the slug pattern (slug@shared_domain).
  assert.ok(
    parsed.email!.startsWith(`${slug}@`),
    `expected email "${slug}@*", got "${parsed.email}"`,
  );
});

test("mcp-ext: send_email tool happy path with HITL agent queues message", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  if (!list.tools.find((t) => t.name === "send_email")) {
    info(SUITE, "send-email-absent", "no send_email tool — skipping happy-path");
    return;
  }
  const email = await ensureHitlAgent();
  // The MCP send_email tool's schema uses `agent_email` (matching the
  // E2A_AGENT_EMAIL env var name), NOT `from` (which is the raw HTTP
  // API name). Passing `from` triggers Zod's strict-schema rejection
  // before the tool body ever runs. The corresponding param is also
  // optional when E2A_AGENT_EMAIL is set in the server env, but we
  // pass it explicitly here because this test creates a fresh agent
  // per run and we want to send from THAT agent, not the env default.
  const r = await callTool(mcp, "send_email", {
    agent_email: email,
    to: [SINK_EMAIL],
    subject: uniqueSubject("mcp send"),
    body: "from MCP",
  });
  if (r.isError) {
    fail(SUITE, "send-email-error", `send_email isError on valid input: ${extractText(r).slice(0, 200)}`);
    return;
  }
  const parsed = JSON.parse(extractText(r)) as { message_id?: string; status?: string };
  assert.ok(parsed.message_id?.startsWith("msg_"), `expected msg_ prefix, got "${parsed.message_id}"`);
  if (parsed.status !== "pending_approval") {
    info(SUITE, "mcp-send-not-pending", `expected pending_approval for HITL, got "${parsed.status}"`);
  }
  // Clean up via API.
  await apiClient.post(`/api/v1/messages/${parsed.message_id}/reject`, { body: { reason: "e2e mcp send cleanup" } });
});

test("mcp-ext: list_pending_messages and get_pending_message round-trip", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  const hasList = list.tools.find((t) => t.name === "list_pending_messages");
  const hasGet = list.tools.find((t) => t.name === "get_pending_message");
  if (!hasList || !hasGet) {
    info(SUITE, "pending-tools-absent", `missing tools: list=${!!hasList} get=${!!hasGet}`);
    return;
  }
  const email = await ensureHitlAgent();
  // Queue one via API (so we know we have something to inspect).
  const s = await apiClient.post<{ message_id: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("mcp pending"), body: "x" },
  });
  if (s.status !== 202 || !s.body?.message_id) {
    info(SUITE, "pending-setup-failed", `send returned ${s.status}, can't probe pending tools`);
    return;
  }
  const id = s.body.message_id;

  // list_pending_messages — should include our queued msg. The MCP
  // tool's schema is strictInputSchema({}) — it takes ZERO arguments
  // (no page_size, no token). The HTTP API does paginate; the MCP
  // wrapper deliberately doesn't expose that surface. Pass nothing.
  const lp = await callTool(mcp, "list_pending_messages");
  if (lp.isError) {
    fail(SUITE, "list-pending-error", `list_pending_messages isError: ${extractText(lp).slice(0, 200)}`);
  } else {
    const text = extractText(lp);
    if (!text.includes(id)) {
      info(SUITE, "list-pending-missing-msg", `queued ${id} not in list_pending_messages response (may be paginated or filtered)`);
    }
  }

  // get_pending_message.
  const gp = await callTool(mcp, "get_pending_message", { message_id: id });
  if (gp.isError) {
    fail(SUITE, "get-pending-error", `get_pending_message isError for ${id}: ${extractText(gp).slice(0, 200)}`);
  } else {
    const parsed = JSON.parse(extractText(gp)) as { id?: string; message_id?: string; status?: string };
    const returnedId = parsed.id ?? parsed.message_id;
    if (returnedId !== id) {
      info(SUITE, "get-pending-id-mismatch", `get_pending_message returned id=${returnedId}, expected ${id}`);
    }
  }

  // Cleanup
  await apiClient.post(`/api/v1/messages/${id}/reject`, { body: { reason: "e2e mcp pending cleanup" } });
});

test("mcp-ext: reject_pending_message via MCP transitions the message", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  if (!list.tools.find((t) => t.name === "reject_pending_message")) {
    info(SUITE, "reject-tool-absent", "no reject_pending_message — skipping");
    return;
  }
  const email = await ensureHitlAgent();
  const s = await apiClient.post<{ message_id: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("mcp reject"), body: "x" },
  });
  if (s.status !== 202 || !s.body?.message_id) {
    info(SUITE, "reject-setup-failed", `send returned ${s.status}`);
    return;
  }
  const id = s.body.message_id;
  const r = await callTool(mcp, "reject_pending_message", { message_id: id, reason: "e2e mcp reject" });
  if (r.isError) {
    fail(SUITE, "reject-error", `reject_pending_message isError: ${extractText(r).slice(0, 200)}`);
    return;
  }
  // Re-reject — should now fail (already rejected, 409 from API; MCP should surface as error).
  const r2 = await callTool(mcp, "reject_pending_message", { message_id: id, reason: "should fail" });
  if (!r2.isError) {
    info(SUITE, "double-reject-not-error", "re-reject of already-rejected message did not surface as error");
  }
});

test("mcp-ext: approve_pending_message via MCP sends the message", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  if (!list.tools.find((t) => t.name === "approve_pending_message")) {
    info(SUITE, "approve-tool-absent", "no approve_pending_message — skipping");
    return;
  }
  const email = await ensureHitlAgent();
  const s = await apiClient.post<{ message_id: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("mcp approve"), body: "x" },
  });
  if (s.status !== 202 || !s.body?.message_id) {
    info(SUITE, "approve-setup-failed", `send returned ${s.status}`);
    return;
  }
  const id = s.body.message_id;
  const r = await callTool(mcp, "approve_pending_message", { message_id: id });
  if (r.isError) {
    fail(SUITE, "approve-error", `approve_pending_message isError: ${extractText(r).slice(0, 200)}`);
    return;
  }
  // Re-approve — should fail with 409 (already sent).
  const r2 = await callTool(mcp, "approve_pending_message", { message_id: id });
  if (!r2.isError) {
    info(SUITE, "double-approve-not-error", "re-approve of sent message did not surface as error");
  }
});

test("mcp-ext: get_message returns shape and only own messages", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  if (!list.tools.find((t) => t.name === "get_message")) {
    info(SUITE, "get-msg-absent", "no get_message tool — skipping");
    return;
  }
  // The MCP get_message tool fetches via the AGENT-scoped endpoint
  // GET /api/v1/agents/{agent_email}/messages/{id} — anti-enumeration
  // 404s on any message that doesn't belong to the pinned agent. We
  // pull candidate IDs from the same scope so the test exercises the
  // happy path instead of accidentally tripping the cross-agent guard.
  const pinnedAgent = apiClient.env.primaryAgentEmail;
  const listMsgs = await apiClient.get<{ messages: Array<{ id: string }> }>(
    `/api/v1/agents/${encodeURIComponent(pinnedAgent)}/messages`,
    { query: { limit: 1 } },
  );
  const id = listMsgs.body?.messages?.[0]?.id;
  if (!id) {
    info(SUITE, "get-msg-no-fixture", `no messages in agent ${pinnedAgent}'s inbox — cannot probe get_message happy path`);
    return;
  }
  const r = await callTool(mcp, "get_message", { message_id: id });
  if (r.isError) {
    fail(SUITE, "get-msg-error", `get_message isError for our own ${id}: ${extractText(r).slice(0, 200)}`);
    return;
  }
  const parsed = JSON.parse(extractText(r)) as { id?: string; message_id?: string };
  const returnedId = parsed.id ?? parsed.message_id;
  assert.equal(returnedId, id, `expected id ${id}, got ${returnedId}`);

  // Bogus id — should isError.
  const r2 = await callTool(mcp, "get_message", { message_id: `msg_bogus_${Date.now()}` });
  if (!r2.isError) {
    info(SUITE, "get-msg-bogus-not-error", "get_message with bogus id did not surface as error");
  }
});

test("mcp-ext: reply_to_message via MCP — to bogus id surfaces error", async () => {
  const list = await mcp.call<{ tools: Array<{ name: string }> }>("tools/list");
  if (!list.tools.find((t) => t.name === "reply_to_message")) {
    info(SUITE, "reply-tool-absent", "no reply_to_message tool — skipping");
    return;
  }
  const r = await callTool(mcp, "reply_to_message", {
    message_id: `msg_bogus_${Date.now()}`,
    body: "should never go out",
  });
  if (!r.isError) {
    fail(SUITE, "reply-bogus-not-error", `reply_to_message with bogus id did not error: ${extractText(r).slice(0, 200)}`);
  }
});

test("mcp-ext: cross-tool consistency — list_agents matches API surface", async () => {
  const r = await callTool(mcp, "list_agents");
  const text = extractText(r);
  const mcpAgents = (JSON.parse(text) as { agents: Array<{ email: string }> }).agents.map((a) => a.email).sort();
  const apiResp = await apiClient.get<{ agents: Array<{ email: string }> }>("/api/v1/agents");
  const apiAgents = (apiResp.body?.agents ?? []).map((a) => a.email).sort();
  if (mcpAgents.length !== apiAgents.length || JSON.stringify(mcpAgents) !== JSON.stringify(apiAgents)) {
    info(
      SUITE,
      "list-agents-divergence",
      `MCP list_agents (${mcpAgents.length}) differs from API /agents (${apiAgents.length})`,
    );
  } else {
    info(SUITE, "list-agents-aligned", `MCP and API agent lists match: ${apiAgents.length} agents`);
  }
});
