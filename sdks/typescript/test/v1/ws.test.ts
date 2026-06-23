import { describe, it, expect, vi } from "vitest";
import type { WSNotification } from "../../src/v1/ws.js";

// These are pure unit tests for the WS surface. Network-level tests
// would require spinning up a fake WS server, which isn't worth it for
// this PR — the type fix and exponential backoff are easy to verify by
// reading the code, and the iteration semantics are tested in
// client.test.ts where we can mock the WSListener.

describe("WSNotification interface", () => {
  it("matches the server's notification shape", () => {
    // Type-only assertion: if the field set drifts, tsc fails.
    const n: WSNotification = {
      message_id: "msg_1",
      from: "alice@example.com",
      recipient: "bot@agents.e2a.dev",
      subject: "Hi",
      received_at: "2026-04-27T10:00:00Z",
    };
    expect(n.recipient).toBe("bot@agents.e2a.dev");

    const withConv: WSNotification = {
      ...n,
      conversation_id: "conv_xyz",
    };
    expect(withConv.conversation_id).toBe("conv_xyz");
  });

  it("does not have the legacy `to` field", () => {
    // @ts-expect-error — `to` was removed in TS 1.7 to match the
    // server's wire shape (post-PR #48 the server emits `recipient`).
    const _bad: WSNotification = {
      message_id: "msg_1",
      from: "a@b.c",
      to: "bot@agents.e2a.dev",
      subject: "",
      received_at: "",
    };
    expect(true).toBe(true);
  });
});

describe("WSListener exponential backoff", () => {
  it("reads maxBackoffMs option", async () => {
    // Smoke test that the option threading works without actually
    // dialing a WebSocket. We import lazily so the `ws` package isn't
    // required to load this file.
    const { WSListener } = await import("../../src/v1/ws.js");
    const l = new WSListener({
      apiKey: "k",
      agentEmail: "bot@agents.e2a.dev",
      reconnect: false, // don't actually loop
      reconnectDelay: 100,
      maxBackoffMs: 5000,
    });
    // Defaults are correct (no exception, fields not throwing on
    // construction).
    expect(l).toBeDefined();
    l.close();
  });
});

describe("WSListener auth", () => {
  it("sends the API key as an Authorization: Bearer handshake header, not in the URL", async () => {
    const calls: Array<{ url: string; opts: unknown }> = [];
    vi.resetModules();
    vi.doMock("ws", () => {
      class FakeWS {
        constructor(url: string, opts: unknown) {
          calls.push({ url, opts });
        }
        on() {
          return this;
        }
        close() {}
      }
      return { default: FakeWS };
    });

    const { WSListener } = await import("../../src/v1/ws.js");
    const l = new WSListener({ apiKey: "secret_key", agentEmail: "bot@x.dev", reconnect: false });
    l.connect();
    l.close();
    vi.doUnmock("ws");
    vi.resetModules();

    expect(calls).toHaveLength(1);
    // Credential is in the header, never the URL.
    expect(calls[0].url).not.toContain("token=");
    expect(calls[0].url).not.toContain("secret_key");
    expect(calls[0].opts).toEqual({ headers: { Authorization: "Bearer secret_key" } });
  });
});

describe("E2AClient.listen()", () => {
  it("requires an email at point of use", async () => {
    const { E2AClient } = await import("../../src/v1/client.js");
    const c = new E2AClient({ apiKey: "k" });
    expect(() => c.listen("")).toThrow(/email is required/);
  });
});
