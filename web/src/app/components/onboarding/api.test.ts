// Contract guard for the message/review projection. The v1 wire splits
// message state into delivery_status (delivery rollup) + review_status
// (the review/HITL lifecycle: pending_review | sent | review_*). The app
// once read the now-removed hitl_status/`status === "pending_approval"`
// shape, which silently disabled the whole pending queue. These tests
// pin the projection to the v1 field names so that can't recur.

import {
  listAgentMessages,
  listPendingMessages,
  getInboxUnread,
  UNREAD_BADGE_CAP,
} from "./api";

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

  it("maps GET /v1/reviews items into the pending queue (both directions)", async () => {
    mockFetch.mockImplementation((url: string) =>
      url === "/v1/reviews"
        ? okJson({
            items: [
              {
                id: "m1",
                agent: "a@x.com",
                direction: "outbound",
                from: "a@x.com",
                to: ["b@y.com"],
                subject: "held draft",
                review_status: "pending_review",
                created_at: "2026-01-01T00:00:00Z",
              },
              {
                id: "m2",
                agent: "a@x.com",
                direction: "inbound",
                from: "spammer@evil.com",
                to: ["a@x.com"],
                subject: "screened inbound",
                review_status: "pending_review",
                created_at: "2026-01-01T00:00:00Z",
              },
            ],
            next_cursor: null,
          })
        : notFound(),
    );
    const pending = await listPendingMessages();
    expect(pending).toHaveLength(2);
    expect(pending[0]).toMatchObject({ id: "m1", agent_email: "a@x.com", direction: "outbound" });
    expect(pending[1]).toMatchObject({ id: "m2", direction: "inbound", from: "spammer@evil.com" });
  });

  it("does not hit the agent /messages endpoint for the queue (reviews only)", async () => {
    // The queue is a single account-scoped /v1/reviews call — never a
    // per-agent fan-out over /messages (which would never surface inbound).
    mockFetch.mockImplementation((url: string) =>
      url === "/v1/reviews" ? okJson({ items: [], next_cursor: null }) : notFound(),
    );
    await listPendingMessages();
    const urls = mockFetch.mock.calls.map((c) => c[0] as string);
    expect(urls).toEqual(["/v1/reviews"]);
  });
});

describe("getInboxUnread (Inboxes list badge probe)", () => {
  it("queries inbound unread with the capped limit and reports the count", async () => {
    mockFetch.mockImplementation((url: string) =>
      url.includes("/messages")
        ? okJson({
            items: [
              { message_id: "m1" },
              { message_id: "m2" },
              { message_id: "m3" },
            ],
            next_cursor: null,
          })
        : notFound(),
    );
    const res = await getInboxUnread("billing@acme.dev");
    expect(res).toEqual({ count: 3, more: false });

    // Must filter on the backend's inbound read-state param (read_status),
    // not the ignored `status` param, and only pull one capped page.
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("/v1/agents/billing%40acme.dev/messages");
    expect(url).toContain("direction=inbound");
    expect(url).toContain("read_status=unread");
    expect(url).toContain(`limit=${UNREAD_BADGE_CAP}`);
  });

  it("flags more=true when the capped page is full (cursor present)", async () => {
    // A returned cursor means there are more unread than the cap — the card
    // renders "N+" off this flag.
    mockFetch.mockImplementation((url: string) =>
      url.includes("/messages")
        ? okJson({
            items: Array.from({ length: UNREAD_BADGE_CAP }, (_, i) => ({
              message_id: `m${i}`,
            })),
            next_cursor: "cursor-abc",
          })
        : notFound(),
    );
    const res = await getInboxUnread("billing@acme.dev");
    expect(res.count).toBe(UNREAD_BADGE_CAP);
    expect(res.more).toBe(true);
  });

  it("reports zero unread on an empty page", async () => {
    mockFetch.mockImplementation((url: string) =>
      url.includes("/messages") ? okJson({ items: [], next_cursor: null }) : notFound(),
    );
    expect(await getInboxUnread("billing@acme.dev")).toEqual({ count: 0, more: false });
  });
});
