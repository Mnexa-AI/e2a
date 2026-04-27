import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { E2AApi, E2AApiError } from "../../src/v1/api.js";

const BASE = "http://localhost:9999";

function mockFetch(status: number, body?: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body ?? "")),
  } as Partial<Response> as Response);
}

describe("E2AApi", () => {
  const originalFetch = globalThis.fetch;
  let api: E2AApi;

  beforeEach(() => {
    api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("requires apiKey via arg or env", () => {
    const prev = process.env.E2A_API_KEY;
    delete process.env.E2A_API_KEY;
    try {
      expect(() => new E2AApi({ apiKey: "" })).toThrow(/apiKey is required/);
      expect(() => new E2AApi({})).toThrow(/E2A_API_KEY/);
    } finally {
      if (prev !== undefined) process.env.E2A_API_KEY = prev;
    }
  });

  it("falls back to E2A_API_KEY env var when apiKey not passed", () => {
    const prev = process.env.E2A_API_KEY;
    process.env.E2A_API_KEY = "e2a_from_env";
    try {
      const a = new E2AApi({});
      expect(a.apiKey).toBe("e2a_from_env");
      // Explicit arg still wins.
      const b = new E2AApi({ apiKey: "e2a_explicit" });
      expect(b.apiKey).toBe("e2a_explicit");
    } finally {
      if (prev === undefined) delete process.env.E2A_API_KEY;
      else process.env.E2A_API_KEY = prev;
    }
  });

  it("strips trailing slash from baseUrl", () => {
    const a = new E2AApi({ apiKey: "k", baseUrl: "http://x.dev/" });
    expect(a.baseUrl).toBe("http://x.dev");
  });

  // ── Agents ──────────────────────────────────────────────────

  it("listAgents", async () => {
    const body = { agents: [{ id: "ag_1", email: "bot@test.dev" }] };
    globalThis.fetch = mockFetch(200, body);
    const res = await api.listAgents();
    expect(res.agents).toHaveLength(1);
    expect(res.agents![0].email).toBe("bot@test.dev");
    expect(globalThis.fetch).toHaveBeenCalledWith(
      `${BASE}/api/v1/agents`,
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("registerAgent sends POST", async () => {
    globalThis.fetch = mockFetch(201, { id: "ag_new", email: "new@test.dev" });
    const res = await api.registerAgent({ email: "new@test.dev", agent_mode: "local" });
    expect(res.id).toBe("ag_new");
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[1].method).toBe("POST");
    expect(JSON.parse(call[1].body)).toEqual({ email: "new@test.dev", agent_mode: "local" });
  });

  it("getAgent encodes email in path", async () => {
    globalThis.fetch = mockFetch(200, { id: "ag_1", email: "bot@test.dev" });
    await api.getAgent("bot@test.dev");
    expect(globalThis.fetch).toHaveBeenCalledWith(
      `${BASE}/api/v1/agents/bot%40test.dev`,
      expect.anything(),
    );
  });

  it("deleteAgent returns void", async () => {
    globalThis.fetch = mockFetch(200, { message: "deleted" });
    await expect(api.deleteAgent("bot@test.dev")).resolves.toBeUndefined();
  });

  // ── Messages ────────────────────────────────────────────────

  it("listMessages passes query params", async () => {
    globalThis.fetch = mockFetch(200, { messages: [] });
    await api.listMessages("bot@test.dev", { status: "unread", pageSize: 10 });
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("status=unread");
    expect(url).toContain("page_size=10");
  });

  it("getMessage", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_1" });
    const res = await api.getMessage("bot@test.dev", "msg_1");
    expect(res.message_id).toBe("msg_1");
  });

  it("replyToMessage", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent", message_id: "msg_r1" });
    const res = await api.replyToMessage("bot@test.dev", "msg_1", { body: "thanks" });
    expect(res.status).toBe("sent");
  });

  // ── Domains ─────────────────────────────────────────────────

  it("listDomains", async () => {
    globalThis.fetch = mockFetch(200, { domains: [{ domain: "test.dev" }] });
    const res = await api.listDomains();
    expect(res.domains).toHaveLength(1);
  });

  it("registerDomain", async () => {
    globalThis.fetch = mockFetch(201, { domain: "new.dev", verified: false });
    const res = await api.registerDomain({ domain: "new.dev" });
    expect(res.verified).toBe(false);
  });

  it("deleteDomain", async () => {
    globalThis.fetch = mockFetch(204);
    await expect(api.deleteDomain("test.dev")).resolves.toBeUndefined();
  });

  it("verifyDomain", async () => {
    globalThis.fetch = mockFetch(200, { domain: "test.dev", verified: true });
    const res = await api.verifyDomain("test.dev");
    expect(res.verified).toBe(true);
  });

  // ── Send ────────────────────────────────────────────────────

  it("sendEmail", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent", message_id: "msg_s1" });
    const res = await api.sendEmail({
      from: "bot@test.dev",
      to: "alice@example.com",
      subject: "Hi",
      body: "Hello",
    });
    expect(res.status).toBe("sent");
  });

  // ── HITL (human-in-the-loop approval) ──────────────────────

  it("updateAgent PUTs the body and returns the Agent", async () => {
    globalThis.fetch = mockFetch(200, {
      email: "bot@test.dev",
      hitl_enabled: true,
      hitl_ttl_seconds: 3600,
      hitl_expiration_action: "reject",
    });
    const res = await api.updateAgent("bot@test.dev", {
      hitl_enabled: true,
      hitl_ttl_seconds: 3600,
      hitl_expiration_action: "reject",
    });
    expect(res.hitl_enabled).toBe(true);
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe(`${BASE}/api/v1/agents/bot%40test.dev`);
    expect(call[1].method).toBe("PUT");
    expect(JSON.parse(call[1].body)).toEqual({
      hitl_enabled: true,
      hitl_ttl_seconds: 3600,
      hitl_expiration_action: "reject",
    });
  });

  it("listPendingMessages GETs /api/v1/messages with the filter", async () => {
    globalThis.fetch = mockFetch(200, {
      messages: [{ id: "msg_p1", agent_id: "bot@test.dev", subject: "held" }],
    });
    const res = await api.listPendingMessages();
    expect(res.messages).toHaveLength(1);
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toBe(`${BASE}/api/v1/messages?status=pending_approval`);
  });

  it("getPendingMessage encodes the id and returns the detail", async () => {
    globalThis.fetch = mockFetch(200, {
      id: "msg_x",
      agent_id: "bot@test.dev",
      subject: "held",
      body_text: "preview body",
    });
    const res = await api.getPendingMessage("msg_x");
    expect(res.body_text).toBe("preview body");
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toBe(`${BASE}/api/v1/messages/msg_x`);
  });

  it("approveMessage without overrides POSTs an empty body", async () => {
    globalThis.fetch = mockFetch(200, {
      status: "sent",
      message_id: "msg_x",
      provider_message_id: "<ses@amazonses.com>",
      edited: false,
    });
    const res = await api.approveMessage("msg_x");
    expect(res.status).toBe("sent");
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe(`${BASE}/api/v1/messages/msg_x/approve`);
    expect(call[1].method).toBe("POST");
    expect(JSON.parse(call[1].body)).toEqual({});
  });

  it("approveMessage with overrides forwards them to the server", async () => {
    globalThis.fetch = mockFetch(200, {
      status: "sent",
      message_id: "msg_x",
      edited: true,
    });
    await api.approveMessage("msg_x", {
      subject: "edited",
      to: ["bob@example.com"],
    });
    const body = JSON.parse(
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body,
    );
    expect(body).toEqual({ subject: "edited", to: ["bob@example.com"] });
  });

  it("rejectMessage sends the reason in the body", async () => {
    globalThis.fetch = mockFetch(200, {
      status: "rejected",
      message_id: "msg_x",
      rejection_reason: "bad tone",
    });
    const res = await api.rejectMessage("msg_x", "bad tone");
    expect(res.status).toBe("rejected");
    const body = JSON.parse(
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body,
    );
    expect(body).toEqual({ reason: "bad tone" });
  });

  it("rejectMessage defaults to an empty reason when omitted", async () => {
    globalThis.fetch = mockFetch(200, { status: "rejected", message_id: "msg_x" });
    await api.rejectMessage("msg_x");
    const body = JSON.parse(
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body,
    );
    expect(body).toEqual({ reason: "" });
  });

  // ── Error handling ──────────────────────────────────────────

  it("throws E2AApiError on non-ok response", async () => {
    globalThis.fetch = mockFetch(401, "unauthorized");
    try {
      await api.listAgents();
      expect.unreachable("should throw");
    } catch (err) {
      expect(err).toBeInstanceOf(E2AApiError);
      expect((err as E2AApiError).statusCode).toBe(401);
    }
  });

  it("sets Authorization header", async () => {
    globalThis.fetch = mockFetch(200, {});
    await api.listAgents();
    const headers = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].headers;
    expect(headers.Authorization).toBe("Bearer e2a_test");
  });
});
