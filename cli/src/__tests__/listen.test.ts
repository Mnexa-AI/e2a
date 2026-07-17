import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import type { EmailReceivedData } from "@e2a/sdk/v1";
import { readFileSync } from "node:fs";
import { join } from "node:path";

import {
  isOpenClawUrl,
  extractResponseText,
  handleNotification,
  forwardMessage,
} from "../commands/listen.js";

function mockResponse(body: unknown): Response {
  return {
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(typeof body === "string" ? body : JSON.stringify(body)),
  } as Response;
}

function makeNotification(overrides: Partial<EmailReceivedData> = {}): EmailReceivedData {
  return {
    message_id: "msg_123",
    agent_email: "bot@agents.e2a.dev",
    direction: "inbound",
    from: "alice@example.com",
    authenticated_from: "alice@example.com",
    to: ["bot@agents.e2a.dev"],
    delivered_to: "bot@agents.e2a.dev",
    subject: "Hello",
    auth_headers: {},
    received_at: "2025-01-15T10:30:00Z",
    ...overrides,
  };
}

describe("isOpenClawUrl", () => {
  it("returns true for /v1/responses path", () => {
    expect(isOpenClawUrl("http://localhost:18789/v1/responses")).toBe(true);
  });

  it("returns true for https URLs ending in /v1/responses", () => {
    expect(isOpenClawUrl("https://my-server.com/v1/responses")).toBe(true);
  });

  it("returns false for other paths", () => {
    expect(isOpenClawUrl("http://localhost:3000/webhook")).toBe(false);
  });

  it("returns false for /hooks/agent", () => {
    expect(isOpenClawUrl("http://localhost:18789/hooks/agent")).toBe(false);
  });

  it("returns false for invalid URLs", () => {
    expect(isOpenClawUrl("not-a-url")).toBe(false);
  });

  it("returns false for empty string", () => {
    expect(isOpenClawUrl("")).toBe(false);
  });
});

describe("extractResponseText", () => {
  it("extracts text from a standard OpenAI Responses API response", async () => {
    const res = mockResponse({
      id: "resp_123",
      output: [
        {
          type: "message",
          content: [{ type: "output_text", text: "Hello there!" }],
        },
      ],
    });
    expect(await extractResponseText(res)).toBe("Hello there!");
  });

  it("joins multiple output_text blocks", async () => {
    const res = mockResponse({
      output: [
        {
          type: "message",
          content: [
            { type: "output_text", text: "Part 1" },
            { type: "output_text", text: "Part 2" },
          ],
        },
      ],
    });
    expect(await extractResponseText(res)).toBe("Part 1\nPart 2");
  });

  it("joins text across multiple output messages", async () => {
    const res = mockResponse({
      output: [
        { type: "message", content: [{ type: "output_text", text: "First" }] },
        { type: "message", content: [{ type: "output_text", text: "Second" }] },
      ],
    });
    expect(await extractResponseText(res)).toBe("First\nSecond");
  });

  it("ignores non-message output items", async () => {
    const res = mockResponse({
      output: [
        { type: "function_call", name: "search" },
        { type: "message", content: [{ type: "output_text", text: "Result" }] },
      ],
    });
    expect(await extractResponseText(res)).toBe("Result");
  });

  it("returns null when output is missing", async () => {
    const res = mockResponse({ id: "resp_123" });
    expect(await extractResponseText(res)).toBeNull();
  });

  it("returns null when output is empty", async () => {
    const res = mockResponse({ output: [] });
    expect(await extractResponseText(res)).toBeNull();
  });

  it("returns null on malformed JSON", async () => {
    const res = {
      json: () => Promise.reject(new Error("bad json")),
    } as Response;
    expect(await extractResponseText(res)).toBeNull();
  });
});

describe("listen notification handling", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("prints a human-readable notification by default", async () => {
    const client = {
      messages: { get: vi.fn(), reply: vi.fn() },
    } as any;

    await handleNotification(
      client,
      "bot@agents.e2a.dev",
      makeNotification(),
      {},
    );

    expect(mockStdout).toHaveBeenCalledWith(
      expect.stringContaining("From: alice@example.com | Subject: Hello"),
    );
    expect(client.messages.get).not.toHaveBeenCalled();
  });

  it("fetches and prints raw JSON for --json mode", async () => {
    const full = {
      id: "msg_123",
      from_: "alice@example.com",
      delivered_to: "bot@agents.e2a.dev",
      subject: "Hello",
      rawMessage: "U3ViamVjdDogSGVsbG8NCg0KSGkgdGhlcmUh",
    };
    const client = {
      messages: { get: vi.fn().mockResolvedValue(full), reply: vi.fn() },
    } as any;

    await handleNotification(
      client,
      "bot@agents.e2a.dev",
      makeNotification(),
      { json: true },
    );

    expect(client.messages.get).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_123");
    expect(mockStdout).toHaveBeenCalledWith(`${JSON.stringify(full)}\n`);
  });

  it("forwards exact raw JSON to a generic webhook", async () => {
    const full = {
      id: "msg_123",
      from_: "alice@example.com",
      delivered_to: "bot@agents.e2a.dev",
      subject: "Hello",
      rawMessage: "U3ViamVjdDogSGVsbG8NCg0KSGkgdGhlcmUh",
    };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: () => Promise.resolve("ok"),
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = {
      messages: { get: vi.fn().mockResolvedValue(full), reply: vi.fn() },
    } as any;

    await forwardMessage(
      client,
      "bot@agents.e2a.dev",
      makeNotification(),
      "https://example.com/webhook",
      "secret",
    );

    expect(client.messages.get).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_123");
    expect(fetchMock).toHaveBeenCalledWith(
      "https://example.com/webhook",
      expect.objectContaining({
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: "Bearer secret",
        },
        body: JSON.stringify(full),
      }),
    );
    expect(client.messages.reply).not.toHaveBeenCalled();
    expect(mockStderr).toHaveBeenCalledWith(
      "Forwarded msg_123 to https://example.com/webhook\n",
    );
  });

  it("forwards AND prints JSON when --forward and --json are both set (regression: silent forward drop)", async () => {
    const full = {
      id: "msg_123",
      from_: "alice@example.com",
      delivered_to: "bot@agents.e2a.dev",
      subject: "Hello",
      rawMessage: "U3ViamVjdDogSGVsbG8NCg0KSGkgdGhlcmUh",
    };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: () => Promise.resolve("ok"),
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = {
      messages: { get: vi.fn().mockResolvedValue(full), reply: vi.fn() },
    } as any;

    await handleNotification(client, "bot@agents.e2a.dev", makeNotification(), {
      json: true,
      forward: "https://example.com/webhook",
      forwardToken: "secret",
    });

    // The forward is a side channel: it MUST fire even though --json is also
    // set (before the fix, --json short-circuited and the forward was dropped).
    expect(fetchMock).toHaveBeenCalledWith(
      "https://example.com/webhook",
      expect.objectContaining({ method: "POST", body: JSON.stringify(full) }),
    );
    // ...and the JSON rendering still reaches stdout.
    expect(mockStdout).toHaveBeenCalledWith(`${JSON.stringify(full)}\n`);
  });

  it("prints JSON when the independent forward side channel rejects", async () => {
    const full = {
      id: "msg_123",
      subject: "Hello",
    };
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new Error("connection refused")),
    );
    const client = {
      messages: { get: vi.fn().mockResolvedValue(full), reply: vi.fn() },
    } as any;

    await expect(
      handleNotification(client, "bot@agents.e2a.dev", makeNotification(), {
        json: true,
        forward: "http://127.0.0.1:1/webhook",
      }),
    ).resolves.toBeUndefined();

    expect(mockStdout).toHaveBeenCalledWith(`${JSON.stringify(full)}\n`);
    expect(mockStderr).toHaveBeenCalledWith(
      "Forward failed: connection refused\n",
    );
  });

  it("forwards to OpenClaw and auto-replies when text is returned", async () => {
    // No parsed/body text — exercise the rawMessage decode fallback.
    const raw = "Subject: Hello\r\n\r\nHi there!";
    const full = {
      id: "msg_123",
      from_: "alice@example.com",
      delivered_to: "bot@agents.e2a.dev",
      subject: "Hello",
      body: { text: "", html: "" },
      parsed: { text: "", truncated: false },
      rawMessage: Buffer.from(raw, "utf-8").toString("base64"),
    };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          output: [
            {
              type: "message",
              content: [{ type: "output_text", text: "Thanks for the email." }],
            },
          ],
        }),
      text: () => Promise.resolve("ok"),
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = {
      messages: {
        get: vi.fn().mockResolvedValue(full),
        reply: vi.fn().mockResolvedValue({
          status: "sent",
          messageId: "msg_reply_1",
        }),
      },
    } as any;

    await forwardMessage(
      client,
      "bot@agents.e2a.dev",
      makeNotification(),
      "https://openclaw.local/v1/responses",
      "openclaw-token",
    );

    expect(fetchMock).toHaveBeenCalledWith(
      "https://openclaw.local/v1/responses",
      expect.objectContaining({
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: "Bearer openclaw-token",
        },
        body: JSON.stringify({
          model: "openclaw",
          input: "New email from alice@example.com\n\nSubject: Hello\n\nHi there!",
        }),
      }),
    );
    expect(client.messages.reply).toHaveBeenCalledWith(
      "bot@agents.e2a.dev",
      "msg_123",
      { text: "Thanks for the email." },
    );
    expect(mockStderr).toHaveBeenCalledWith(
      "Replied to alice@example.com (msg_123)\n",
    );
  });
});

describe("listen --once forwarding failures", () => {
  afterEach(() => {
    vi.doUnmock("../sdk.js");
    vi.resetModules();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders the matching message and exits after a forward transport error", async () => {
    vi.resetModules();

    const full = { id: "msg_123", subject: "Hello" };
    const fakeStream = {
      on: vi.fn(),
      close: vi.fn(),
      async *[Symbol.asyncIterator]() {
        yield {
          type: "email.received",
          id: "evt_123",
          schema_version: "1",
          created_at: "2025-01-15T10:30:00Z",
          data: makeNotification(),
        };
      },
    };
    const client = {
      listen: vi.fn(() => fakeStream),
      messages: {
        get: vi.fn().mockResolvedValue(full),
        reply: vi.fn(),
      },
    };
    vi.doMock("../sdk.js", () => ({
      createClient: () => client,
      requireAgentEmail: (agent?: string) =>
        agent ?? "bot@agents.e2a.dev",
    }));
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new Error("connection refused")),
    );
    const stdoutSpy = vi
      .spyOn(process.stdout, "write")
      .mockImplementation(() => true);
    const stderrSpy = vi
      .spyOn(process.stderr, "write")
      .mockImplementation(() => true);

    const { listen } = await import("../commands/listen.js");
    await listen({
      agent: "bot@agents.e2a.dev",
      once: true,
      json: true,
      forward: "http://127.0.0.1:1/webhook",
    });

    expect(stdoutSpy).toHaveBeenCalledTimes(1);
    expect(stdoutSpy).toHaveBeenCalledWith(`${JSON.stringify(full)}\n`);
    expect(stderrSpy).toHaveBeenCalledWith(
      "Forward failed: connection refused\n",
    );
    expect(stderrSpy).not.toHaveBeenCalledWith(
      expect.stringContaining("Error handling message"),
    );
    expect(fakeStream.close).toHaveBeenCalled();
  });
});

describe("listen replaced-takeover exit (WS close 4000)", () => {
  afterEach(() => {
    vi.doUnmock("../sdk.js");
    vi.resetModules();
    vi.restoreAllMocks();
  });

  it("prints a clear message and exits EXIT.REQUEST (5) — never retry-loops", async () => {
    vi.resetModules();

    // Build the fake stream first; wire the typed error from the SAME module
    // registry the re-imported listen.js will use, so instanceof matches.
    vi.doMock("../sdk.js", () => ({
      createClient: () => ({ listen: () => fakeStream }),
      requireAgentEmail: (a?: string) => a ?? "bot@agents.e2a.dev",
    }));

    const sdk = await import("@e2a/sdk/v1");
    const closeContract = JSON.parse(readFileSync(
      join(__dirname, "../../../internal/ws/testdata/close-contract.json"),
      "utf8",
    )) as Array<{ code: number; reason: string; classification: string }>;
    const replacement = closeContract.find((entry) => entry.classification === "replaced");
    expect(replacement).toEqual({ code: 4000, reason: "replaced", classification: "replaced" });
    const replaced = new sdk.E2AConnectionReplacedError({
      code: "ws_replaced",
      message: "a newer connection for this agent superseded this one",
      status: 0,
      retryable: false,
    });

    const fakeStream = {
      on: vi.fn(),
      close: vi.fn(),
      [Symbol.asyncIterator]() {
        // The SDK stream rejects the pending next() with the typed error
        // after it stops reconnecting on close code 4000.
        return { next: () => Promise.reject(replaced) };
      },
    };

    const stderrSpy = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const exitSpy = vi.spyOn(process, "exit").mockImplementation(((code?: number) => {
      throw new Error(`process.exit(${code})`);
    }) as never);

    const { listen } = await import("../commands/listen.js");
    await expect(listen({ agent: "bot@agents.e2a.dev" })).rejects.toThrow("process.exit(5)");

    expect(exitSpy).toHaveBeenCalledWith(5); // EXIT.REQUEST — do NOT retry
    expect(fakeStream.close).toHaveBeenCalled();
    const said = stderrSpy.mock.calls.map((c) => String(c[0])).join("");
    expect(said).toContain("listener replaced");
    expect(said).toContain("4000");
    expect(said).toContain("one connection per agent");
    // The generic connection-error line must not double-report it.
    expect(said).not.toContain("Connection error:");
  });
});
