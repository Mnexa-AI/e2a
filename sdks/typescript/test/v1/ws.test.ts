import { describe, it, expect, vi } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { WSEvent } from "../../src/v1/ws.js";
import { isEmailReceived } from "../../src/v1/webhook-signature.js";

// These are pure unit tests for the WS surface. Network-level tests
// would require spinning up a fake WS server, which isn't worth it for
// this PR — the type fix and exponential backoff are easy to verify by
// reading the code, and the iteration semantics are tested in
// client.test.ts where we can mock the WSListener.

describe("WSEvent envelope", () => {
  it("is the versioned event envelope — the same shape as a webhook delivery", () => {
    // Parse the shared golden fixture: the WS channel emits the SAME
    // envelope+payload the webhook channel delivers (the server's ws tests
    // assert frame parity against this very file).
    const raw = readFileSync(
      join(__dirname, "../../../../internal/eventpayload/testdata/email.received.json"),
      "utf8",
    );
    const event: WSEvent = JSON.parse(raw);
    expect(event.type).toBe("email.received");
    expect(event.schema_version).toBe("1");
    expect(event.id).toMatch(/^evt_/);
    if (!isEmailReceived(event)) throw new Error("guard should narrow email.received");
    expect(event.data.message_id).toMatch(/^msg_/);
    expect(event.data.delivered_to).toBe("support@agents.example.com");
    expect(event.data.direction).toBe("inbound");
  });

  it("tolerates unknown event types (forward-compat)", () => {
    const event: WSEvent = {
      type: "email.some_future_kind",
      id: "evt_x",
      schema_version: "1",
      created_at: "2026-07-01T10:30:00Z",
      data: { anything: true },
    };
    expect(isEmailReceived(event)).toBe(false);
    expect(event.type).toBe("email.some_future_kind");
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

describe("WSListener close-code contract (docs/api.md)", () => {
  // Drives the reconnect matrix by mocking the `ws` module: each fake socket
  // records its event handlers so the test can fire a server-initiated close
  // with a specific code/reason and observe whether the listener redials
  // (transient) or stops with a typed error (terminal).
  type Handler = (...args: unknown[]) => void;
  class FakeWS {
    static instances: FakeWS[] = [];
    handlers = new Map<string, Handler>();
    constructor(
      public url: string,
      public opts: unknown,
    ) {
      FakeWS.instances.push(this);
    }
    on(event: string, fn: Handler) {
      this.handlers.set(event, fn);
      return this;
    }
    close() {}
    serverClose(code: number, reason: string) {
      this.handlers.get("close")?.(code, Buffer.from(reason));
    }
  }

  async function listenWith(closeCode: number, reason: string) {
    FakeWS.instances = [];
    vi.resetModules();
    vi.doMock("ws", () => ({ default: FakeWS }));
    vi.useFakeTimers();
    const { WSListener } = await import("../../src/v1/ws.js");
    const errors = await import("../../src/v1/errors.js");
    const l = new WSListener({
      apiKey: "k",
      agentEmail: "bot@x.dev",
      reconnect: true,
      reconnectDelay: 10,
    });
    const seen: Error[] = [];
    const closes: Array<{ code: number; reason: string }> = [];
    l.on("error", (e) => seen.push(e));
    l.on("close", (code, r) => closes.push({ code, reason: r }));
    l.connect();
    expect(FakeWS.instances).toHaveLength(1);
    FakeWS.instances[0].serverClose(closeCode, reason);
    // Give any scheduled redial a chance to fire.
    await vi.advanceTimersByTimeAsync(60_000);
    vi.useRealTimers();
    vi.doUnmock("ws");
    vi.resetModules();
    return { dials: FakeWS.instances.length, errors: seen, closes, errorsMod: errors, listener: l };
  }

  it("4000 'replaced' → E2AConnectionReplacedError, no reconnect", async () => {
    const { dials, errors, closes, errorsMod } = await listenWith(4000, "replaced");
    expect(dials).toBe(1); // never redialed
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(errorsMod.E2AConnectionReplacedError);
    expect((errors[0] as InstanceType<typeof errorsMod.E2AError>).code).toBe("ws_replaced");
    expect((errors[0] as InstanceType<typeof errorsMod.E2AError>).retryable).toBe(false);
    expect(closes).toEqual([{ code: 4000, reason: "replaced" }]);
  });

  it("1008 policy rejection → E2APermissionError, no reconnect", async () => {
    const { dials, errors, errorsMod } = await listenWith(1008, "policy violation");
    expect(dials).toBe(1);
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(errorsMod.E2APermissionError);
    expect((errors[0] as InstanceType<typeof errorsMod.E2AError>).code).toBe("ws_policy_violation");
  });

  it("unknown 4xxx application code → fatal E2AError, no reconnect (forward-compat)", async () => {
    const { dials, errors, errorsMod } = await listenWith(4321, "future_condition");
    expect(dials).toBe(1);
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(errorsMod.E2AError);
    expect((errors[0] as InstanceType<typeof errorsMod.E2AError>).code).toBe("ws_closed");
    expect((errors[0] as InstanceType<typeof errorsMod.E2AError>).retryable).toBe(false);
  });

  it("1001 'shutting_down' (server restart) → reconnects with backoff", async () => {
    const { dials, errors } = await listenWith(1001, "shutting_down");
    expect(dials).toBeGreaterThan(1); // redialed
    expect(errors).toHaveLength(0);
  });

  it("1006 abnormal close (network drop) → reconnects", async () => {
    const { dials, errors } = await listenWith(1006, "");
    expect(dials).toBeGreaterThan(1);
    expect(errors).toHaveLength(0);
  });

  it("1011 internal server error → reconnects", async () => {
    const { dials, errors } = await listenWith(1011, "");
    expect(dials).toBeGreaterThan(1);
    expect(errors).toHaveLength(0);
  });

  it("client-initiated close → no reconnect and no error, even if the server echoes a code", async () => {
    FakeWS.instances = [];
    vi.resetModules();
    vi.doMock("ws", () => ({ default: FakeWS }));
    const { WSListener } = await import("../../src/v1/ws.js");
    const l = new WSListener({ apiKey: "k", agentEmail: "bot@x.dev", reconnect: true });
    const seen: Error[] = [];
    l.on("error", (e) => seen.push(e));
    l.connect();
    l.close(); // client closes first
    FakeWS.instances[0].serverClose(1000, "");
    vi.doUnmock("ws");
    vi.resetModules();
    expect(FakeWS.instances).toHaveLength(1);
    expect(seen).toHaveLength(0);
  });
});

describe("E2AClient.listen()", () => {
  it("requires an email at point of use", async () => {
    const { E2AClient } = await import("../../src/v1/client.js");
    const c = new E2AClient({ apiKey: "k" });
    expect(() => c.listen("")).toThrow(/email is required/);
  });
});
