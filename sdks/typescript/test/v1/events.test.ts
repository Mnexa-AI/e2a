import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { E2AApi } from "../../src/v1/api.js";

// Unit tests for the slice 6/7/8 events SDK surface — listEvents,
// getEvent, redeliverEvent, redeliverWebhookSince. Mocks fetch and
// asserts the wire shape (URL, query params, body) sent by each method.

const BASE = "http://localhost:9999";

function mockFetch(status: number, body?: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body ?? "")),
  } as Partial<Response> as Response);
}

describe("E2AApi events surface", () => {
  const originalFetch = globalThis.fetch;
  let api: E2AApi;

  beforeEach(() => {
    api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  describe("listEvents", () => {
    it("hits /api/v1/events with no params by default", async () => {
      const fetchMock = mockFetch(200, { events: [], next_token: "" });
      globalThis.fetch = fetchMock;
      await api.listEvents();
      const [url] = fetchMock.mock.calls[0];
      expect(url).toBe(`${BASE}/api/v1/events`);
    });

    it("encodes filter params", async () => {
      const fetchMock = mockFetch(200, { events: [] });
      globalThis.fetch = fetchMock;
      await api.listEvents({
        type: "email.received",
        agentId: "ag_x",
        conversationId: "conv_y",
        messageId: "msg_z",
        since: "2026-06-01T00:00:00Z",
        until: "2026-06-02T00:00:00Z",
        pageSize: 25,
        token: "opaque",
      });
      const [url] = fetchMock.mock.calls[0];
      const u = new URL(url as string);
      expect(u.searchParams.get("type")).toBe("email.received");
      expect(u.searchParams.get("agent_id")).toBe("ag_x");
      expect(u.searchParams.get("conversation_id")).toBe("conv_y");
      expect(u.searchParams.get("message_id")).toBe("msg_z");
      expect(u.searchParams.get("since")).toBe("2026-06-01T00:00:00Z");
      expect(u.searchParams.get("until")).toBe("2026-06-02T00:00:00Z");
      expect(u.searchParams.get("page_size")).toBe("25");
      expect(u.searchParams.get("token")).toBe("opaque");
    });

    it("returns parsed events + next_token", async () => {
      const sample = {
        events: [
          { id: "evt_a", type: "email.received", created_at: "2026-06-01T12:00:00Z", status: "processed", data: { x: 1 } },
          { id: "evt_b", type: "email.sent", created_at: "2026-06-01T11:00:00Z", status: "processed", data: {} },
        ],
        next_token: "next_cursor",
      };
      globalThis.fetch = mockFetch(200, sample);
      const res = await api.listEvents();
      expect(res.events).toHaveLength(2);
      expect(res.events![0].id).toBe("evt_a");
      expect(res.next_token).toBe("next_cursor");
    });

    it("includes Authorization header", async () => {
      const fetchMock = mockFetch(200, { events: [] });
      globalThis.fetch = fetchMock;
      await api.listEvents();
      const [, init] = fetchMock.mock.calls[0];
      const headers = (init as RequestInit).headers as Record<string, string>;
      expect(headers["Authorization"]).toBe("Bearer e2a_test");
    });
  });

  describe("getEvent", () => {
    it("hits /api/v1/events/{id}", async () => {
      const fetchMock = mockFetch(200, { id: "evt_abc", type: "email.received" });
      globalThis.fetch = fetchMock;
      const e = await api.getEvent("evt_abc");
      const [url, init] = fetchMock.mock.calls[0];
      expect(url).toBe(`${BASE}/api/v1/events/evt_abc`);
      expect((init as RequestInit).method).toBe("GET");
      expect(e.id).toBe("evt_abc");
    });

    it("URL-encodes the event id", async () => {
      const fetchMock = mockFetch(200, {});
      globalThis.fetch = fetchMock;
      await api.getEvent("evt with spaces");
      const [url] = fetchMock.mock.calls[0];
      expect(url).toBe(`${BASE}/api/v1/events/evt%20with%20spaces`);
    });

    it("surfaces 404 as E2AApiError", async () => {
      globalThis.fetch = mockFetch(404, "not found");
      await expect(api.getEvent("evt_missing")).rejects.toThrow();
    });

    it("surfaces 410 as E2AApiError", async () => {
      globalThis.fetch = mockFetch(410, "gone");
      await expect(api.getEvent("evt_expired")).rejects.toThrow();
    });
  });

  describe("redeliverEvent", () => {
    it("sends empty body by default (fan-out)", async () => {
      const fetchMock = mockFetch(200, { event_id: "evt_x", deliveries: [] });
      globalThis.fetch = fetchMock;
      await api.redeliverEvent("evt_x");
      const [url, init] = fetchMock.mock.calls[0];
      expect(url).toBe(`${BASE}/api/v1/events/evt_x/redeliver`);
      expect((init as RequestInit).method).toBe("POST");
      const body = JSON.parse((init as RequestInit).body as string);
      expect(body).toEqual({});
    });

    it("sends webhook_id when provided (targeted)", async () => {
      const fetchMock = mockFetch(200, {});
      globalThis.fetch = fetchMock;
      await api.redeliverEvent("evt_x", { webhookId: "wh_target" });
      const [, init] = fetchMock.mock.calls[0];
      const body = JSON.parse((init as RequestInit).body as string);
      expect(body).toEqual({ webhook_id: "wh_target" });
    });

    it("surfaces 409 (webhook not originally matched)", async () => {
      globalThis.fetch = mockFetch(409, "not matched");
      await expect(api.redeliverEvent("evt_x", { webhookId: "wh_other" })).rejects.toThrow();
    });
  });

  describe("redeliverWebhookSince", () => {
    it("posts {since} to /webhooks/{id}/redeliver-since", async () => {
      const fetchMock = mockFetch(200, { scheduled: 5, skipped_already_pending: 0 });
      globalThis.fetch = fetchMock;
      await api.redeliverWebhookSince("wh_target", "2026-06-01T00:00:00Z");
      const [url, init] = fetchMock.mock.calls[0];
      expect(url).toBe(`${BASE}/api/v1/webhooks/wh_target/redeliver-since`);
      expect((init as RequestInit).method).toBe("POST");
      const body = JSON.parse((init as RequestInit).body as string);
      expect(body).toEqual({ since: "2026-06-01T00:00:00Z" });
    });

    it("surfaces 400 when since is out of window", async () => {
      globalThis.fetch = mockFetch(400, "too old");
      await expect(api.redeliverWebhookSince("wh_x", "2020-01-01T00:00:00Z")).rejects.toThrow();
    });
  });

  describe("concurrent calls", () => {
    it("handles 20 simultaneous listEvents calls", async () => {
      const fetchMock = mockFetch(200, { events: [{ id: "evt_1", type: "email.received", created_at: "2026-06-01T00:00:00Z", status: "processed", data: {} }] });
      globalThis.fetch = fetchMock;
      const results = await Promise.all(
        Array.from({ length: 20 }, () => api.listEvents()),
      );
      expect(results).toHaveLength(20);
      for (const r of results) {
        expect(r.events).toHaveLength(1);
      }
      expect(fetchMock).toHaveBeenCalledTimes(20);
    });

    it("handles concurrent listEvents + getEvent + redeliverEvent", async () => {
      const fetchMock = mockFetch(200, {});
      globalThis.fetch = fetchMock;
      await Promise.all([
        api.listEvents({ type: "email.received" }),
        api.getEvent("evt_a"),
        api.redeliverEvent("evt_b", { webhookId: "wh_x" }),
        api.listEvents({ type: "email.sent" }),
        api.getEvent("evt_c"),
      ]);
      expect(fetchMock).toHaveBeenCalledTimes(5);
    });
  });
});
