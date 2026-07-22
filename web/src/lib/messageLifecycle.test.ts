import { getMessageLifecycle, parseMessageLifecyclePage } from "./messageLifecycle";

describe("message lifecycle data helper", () => {
  afterEach(() => {
    jest.restoreAllMocks();
    delete (global as typeof globalThis & { fetch?: typeof fetch }).fetch;
  });

  it("fetches the encoded agent/message subresource with cursor and limit", async () => {
    const payload = {
      items: [{
        id: "mlt_1",
        message_id: "msg/1",
        direction: "outbound",
        recipient: null,
        stage: "delivery",
        outcome: "delivered",
        reason_code: "delivery.recipient_server_accepted",
        retryable: false,
        evidence: { smtp: { code: 250 }, extension: true },
        correlation_ids: { provider_message_id: "provider/1" },
        occurred_at: "2026-07-21T12:00:00Z",
        reconstructed: false,
      }],
      next_cursor: "next/cursor",
    };
    const fetchMock = jest.fn().mockResolvedValue({
      ok: true,
      json: async () => payload,
    } as Response);
    Object.defineProperty(global, "fetch", {
      configurable: true,
      writable: true,
      value: fetchMock,
    });

    await expect(getMessageLifecycle("ops+bot@example.com", "msg/1", {
      cursor: "cursor/1+two",
      limit: 25,
    })).resolves.toEqual(payload);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/agents/ops%2Bbot%40example.com/messages/msg%2F1/lifecycle?cursor=cursor%2F1%2Btwo&limit=25",
      { credentials: "include" },
    );
  });

  it("accepts omitted recipients, nullable cursors, and open evidence/correlation maps", () => {
    expect(parseMessageLifecyclePage({
      items: [{
        id: "mlt_2",
        message_id: "msg_2",
        direction: "inbound",
        stage: "authentication",
        outcome: "passed",
        reason_code: "authentication.dmarc_pass",
        retryable: false,
        evidence: { future_detail: [1, 2, 3] },
        correlation_ids: { future_id: "abc" },
        occurred_at: "2026-07-21T12:00:00Z",
        reconstructed: true,
      }],
      next_cursor: null,
    }).items[0]).not.toHaveProperty("recipient");
  });

  it("rejects values outside the closed lifecycle vocabulary", () => {
    const base = {
      id: "mlt_1", message_id: "msg_1", direction: "inbound",
      stage: "accepted", outcome: "accepted", reason_code: "acceptance.inbound_smtp",
      retryable: false, evidence: {}, correlation_ids: {},
      occurred_at: "2026-07-21T12:00:00Z", reconstructed: false,
    };
    expect(() => parseMessageLifecyclePage({ items: [{ ...base, stage: "inbox" }], next_cursor: null }))
      .toThrow(/stage/);
    expect(() => parseMessageLifecyclePage({ items: [{ ...base, reason_code: "screening.prompt_injection" }], next_cursor: null }))
      .toThrow(/reason_code/);
  });
});
