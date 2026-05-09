import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockGetMessage = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agentEmail: "bot@agents.e2a.dev",
    getMessage: mockGetMessage,
  })),
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

  it("displays parsed InboundEmail fields including date", async () => {
    mockGetMessage.mockResolvedValue({
      messageId: "msg_123",
      sender: "alice@example.com",
      recipient: "bot@agents.e2a.dev",
      to: ["bot@agents.e2a.dev"],
      cc: [],
      subject: "Hello",
      textBody: "Hi there!",
      receivedAt: "2025-01-15T10:30:00Z",
    });

    await read("msg_123", undefined);

    expect(mockGetMessage).toHaveBeenCalledWith("msg_123");
    expect(mockStdout).toHaveBeenCalledWith("Message ID: msg_123\n");
    expect(mockStdout).toHaveBeenCalledWith("From: alice@example.com\n");
    expect(mockStdout).toHaveBeenCalledWith("Date: 2025-01-15T10:30:00Z\n");
    expect(mockStdout).toHaveBeenCalledWith("Subject: Hello\n");
    expect(mockStdout).toHaveBeenCalledWith("Hi there!\n");
    // Sole-recipient case: no Also-To or Cc lines emitted.
    const writes: string[] = mockStdout.mock.calls.map((c: unknown[]) => String(c[0]));
    expect(writes.some((w: string) => w.startsWith("Also-To:"))).toBe(false);
    expect(writes.some((w: string) => w.startsWith("Cc:"))).toBe(false);
  });

  it("prints Also-To and Cc when the message had other recipients", async () => {
    mockGetMessage.mockResolvedValue({
      messageId: "msg_group",
      sender: "alice@example.com",
      recipient: "bot-a@agents.e2a.dev",
      // Server-emitted To: header has the agent itself plus another bot.
      to: ["bot-a@agents.e2a.dev", "bot-b@agents.e2a.dev"],
      cc: ["watcher@example.com"],
      subject: "Group",
      textBody: "",
      receivedAt: null,
    });

    await read("msg_group", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Also-To: bot-b@agents.e2a.dev\n");
    expect(mockStdout).toHaveBeenCalledWith("Cc: watcher@example.com\n");
  });

  it("prints Also-To when the agent was Bcc'd (not in the To: header)", async () => {
    mockGetMessage.mockResolvedValue({
      messageId: "msg_bcc",
      sender: "alice@example.com",
      recipient: "bot-bcc@agents.e2a.dev",
      to: ["bot-a@agents.e2a.dev", "bot-b@agents.e2a.dev"],
      cc: [],
      subject: "BCC",
      textBody: "",
      receivedAt: null,
    });

    await read("msg_bcc", undefined);

    expect(mockStdout).toHaveBeenCalledWith(
      "Also-To: bot-a@agents.e2a.dev, bot-b@agents.e2a.dev\n",
    );
  });

  it("shows 'unknown' when receivedAt is null", async () => {
    mockGetMessage.mockResolvedValue({
      messageId: "msg_456",
      sender: "bob@example.com",
      recipient: "bot@agents.e2a.dev",
      to: ["bot@agents.e2a.dev"],
      cc: [],
      subject: "Test",
      textBody: "",
      receivedAt: null,
    });

    await read("msg_456", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Date: unknown\n");
  });
});
