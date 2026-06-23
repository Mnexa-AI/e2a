// Contract guard for the message/review projection. The v1 wire splits
// message state into delivery_status (delivery rollup) + review_status
// (the review/HITL lifecycle: pending_review | sent | review_*). The app
// once read the now-removed hitl_status/`status === "pending_approval"`
// shape, which silently disabled the whole pending queue. These tests
// pin the projection to the v1 field names so that can't recur.

import { listAgentMessages, listPendingMessages } from "./api";

const mockFetch = jest.fn();
beforeEach(() => {
  mockFetch.mockReset();
  global.fetch = mockFetch as unknown as typeof fetch;
});

function okJson(obj: unknown) {
  return Promise.resolve({
    ok: true,
    status: 200,
    text: () => Promise.resolve(JSON.stringify(obj)),
  });
}
function notFound() {
  return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("nf") });
}

describe("message projection (v1 contract)", () => {
  it("maps v1 review_status/delivery_status onto the app fields", async () => {
    mockFetch.mockImplementation((url: string) =>
      url.includes("/messages")
        ? okJson({
            items: [
              {
                message_id: "m1",
                direction: "outbound",
                from: "a@x.com",
                to: ["b@y.com"],
                recipient: "b@y.com",
                subject: "s",
                review_status: "pending_review",
                delivery_status: "queued",
                created_at: "2026-01-01T00:00:00Z",
              },
            ],
            next_cursor: null,
          })
        : notFound(),
    );
    const res = await listAgentMessages("a@x.com", { direction: "outbound" });
    expect(res.items[0].review_status).toBe("pending_review");
    expect(res.items[0].status).toBe("queued"); // delivery rollup
  });

  it("maps inbound read_status (delivery_status is outbound-only)", async () => {
    // Regression: inbound unread state lives in read_status, not status —
    // dropping it silently disabled the inbox's unread/bold affordance.
    mockFetch.mockImplementation((url: string) =>
      url.includes("/messages")
        ? okJson({
            items: [
              {
                message_id: "m1",
                direction: "inbound",
                from: "b@y.com",
                to: ["a@x.com"],
                recipient: "a@x.com",
                subject: "hi",
                read_status: "unread",
                created_at: "2026-01-01T00:00:00Z",
              },
            ],
            next_cursor: null,
          })
        : notFound(),
    );
    const res = await listAgentMessages("a@x.com", { direction: "all" });
    expect(res.items[0].read_status).toBe("unread");
  });

  it("surfaces a v1 pending_review outbound row in the pending queue", async () => {
    mockFetch.mockImplementation((url: string) => {
      if (url === "/v1/agents") return okJson({ items: [{ email: "a@x.com" }] });
      if (url.includes("/messages"))
        return okJson({
          items: [
            {
              message_id: "m1",
              direction: "outbound",
              from: "a@x.com",
              to: ["b@y.com"],
              recipient: "b@y.com",
              subject: "held",
              review_status: "pending_review",
              created_at: "2026-01-01T00:00:00Z",
            },
          ],
          next_cursor: null,
        });
      return notFound();
    });
    const pending = await listPendingMessages();
    expect(pending).toHaveLength(1);
    expect(pending[0].id).toBe("m1");
    expect(pending[0].agent_email).toBe("a@x.com");
  });

  it("ignores a legacy hitl_status/pending_approval row (reads v1 fields only)", async () => {
    // Regression: the pre-migration shape must NOT register as pending —
    // this is the exact drift that silently emptied the queue.
    mockFetch.mockImplementation((url: string) => {
      if (url === "/v1/agents") return okJson({ items: [{ email: "a@x.com" }] });
      if (url.includes("/messages"))
        return okJson({
          items: [
            {
              message_id: "m1",
              direction: "outbound",
              from: "a@x.com",
              to: ["b@y.com"],
              recipient: "b@y.com",
              subject: "legacy",
              hitl_status: "pending_approval",
              status: "pending_approval",
              created_at: "2026-01-01T00:00:00Z",
            },
          ],
          next_cursor: null,
        });
      return notFound();
    });
    const pending = await listPendingMessages();
    expect(pending).toHaveLength(0);
  });
});
