import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockGetMessage = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    messages: { get: mockGetMessage },
  })),
  requireAgentEmail: vi.fn(() => "bot@agents.e2a.dev"),
}));

vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "e2a_testkey",
    api_url: "https://e2a.dev",
    agent_email: "bot@agents.e2a.dev",
  })),
  requireApiKey: vi.fn(() => "e2a_testkey"),
}));

import { read } from "../commands/read.js";

// Build a MessageView-shaped object. `_from` is the generated TS property
// name for the wire field `from`; the body lives under `body`/`parsed`.
function messageView(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    messageId: "msg_123",
    _from: "alice@example.com",
    recipient: "bot@agents.e2a.dev",
    to: ["bot@agents.e2a.dev"],
    cc: [],
    replyTo: [],
    subject: "Hello",
    body: { text: "Hi there!", html: "" },
    parsed: { text: "Hi there!", truncated: false },
    createdAt: "2025-01-15T10:30:00Z",
    ...overrides,
  };
}

describe("read", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockGetMessage.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when messageId is missing", async () => {
    await expect(read(undefined, undefined)).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith("Usage: e2a read <message-id>\n");
  });

  it("displays parsed MessageView fields including date", async () => {
    mockGetMessage.mockResolvedValue(messageView());

    await read("msg_123", undefined);

    expect(mockGetMessage).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_123");
    expect(mockStdout).toHaveBeenCalledWith("Message ID: msg_123\n");
    expect(mockStdout).toHaveBeenCalledWith("From: alice@example.com\n");
    expect(mockStdout).toHaveBeenCalledWith("Date: 2025-01-15T10:30:00Z\n");
    expect(mockStdout).toHaveBeenCalledWith("Subject: Hello\n");
    expect(mockStdout).toHaveBeenCalledWith("Hi there!\n");
    // Sole-recipient case: no Also-To, Cc, or Reply-To lines emitted.
    const writes: string[] = mockStdout.mock.calls.map((c: unknown[]) => String(c[0]));
    expect(writes.some((w: string) => w.startsWith("Also-To:"))).toBe(false);
    expect(writes.some((w: string) => w.startsWith("Cc:"))).toBe(false);
    expect(writes.some((w: string) => w.startsWith("Reply-To:"))).toBe(false);
  });

  it("prints Also-To and Cc when the message had other recipients", async () => {
    mockGetMessage.mockResolvedValue(
      messageView({
        messageId: "msg_group",
        recipient: "bot-a@agents.e2a.dev",
        to: ["bot-a@agents.e2a.dev", "bot-b@agents.e2a.dev"],
        cc: ["watcher@example.com"],
        subject: "Group",
        body: { text: "", html: "" },
        parsed: { text: "", truncated: false },
        createdAt: null,
      }),
    );

    await read("msg_group", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Also-To: bot-b@agents.e2a.dev\n");
    expect(mockStdout).toHaveBeenCalledWith("Cc: watcher@example.com\n");
  });

  it("prints Also-To when the agent was Bcc'd (not in the To: header)", async () => {
    mockGetMessage.mockResolvedValue(
      messageView({
        messageId: "msg_bcc",
        recipient: "bot-bcc@agents.e2a.dev",
        to: ["bot-a@agents.e2a.dev", "bot-b@agents.e2a.dev"],
        cc: [],
        subject: "BCC",
        body: { text: "", html: "" },
        parsed: { text: "", truncated: false },
        createdAt: null,
      }),
    );

    await read("msg_bcc", undefined);

    expect(mockStdout).toHaveBeenCalledWith(
      "Also-To: bot-a@agents.e2a.dev, bot-b@agents.e2a.dev\n",
    );
  });

  it("shows 'unknown' when createdAt is null", async () => {
    mockGetMessage.mockResolvedValue(
      messageView({
        messageId: "msg_456",
        _from: "bob@example.com",
        subject: "Test",
        body: { text: "", html: "" },
        parsed: { text: "", truncated: false },
        createdAt: null,
      }),
    );

    await read("msg_456", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Date: unknown\n");
  });

  it("prints Reply-To when the sender requested a different reply mailbox", async () => {
    // Motivating case: notifications@... with Reply-To: <real-user>.
    mockGetMessage.mockResolvedValue(
      messageView({
        messageId: "msg_granola",
        _from: "notifications@mail.granola.ai",
        replyTo: ["real-user@example.com"],
        subject: "Meeting summary",
        body: { text: "", html: "" },
        parsed: { text: "", truncated: false },
        createdAt: null,
      }),
    );

    await read("msg_granola", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Reply-To: real-user@example.com\n");
  });
});
