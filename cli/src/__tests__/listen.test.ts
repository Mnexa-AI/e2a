import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import type { EmailReceivedData } from "@e2a/sdk/v1";

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
      _from: "alice@example.com",
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
      _from: "alice@example.com",
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

  it("forwards to OpenClaw and auto-replies when text is returned", async () => {
    // No parsed/body text — exercise the rawMessage decode fallback.
    const raw = "Subject: Hello\r\n\r\nHi there!";
    const full = {
      id: "msg_123",
      _from: "alice@example.com",
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
