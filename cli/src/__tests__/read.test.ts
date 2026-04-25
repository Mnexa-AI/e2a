import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockGetMessage = vi.fn();
const mockParse = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agentEmail: "bot@agents.e2a.dev",
    api: {
      getMessage: mockGetMessage,
    },
    parse: mockParse,
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
    mockParse.mockReset();
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
    const detail = {
      message_id: "msg_123",
      from: "alice@example.com",
      to: "bot@agents.e2a.dev",
      subject: "Hello",
      created_at: "2025-01-15T10:30:00Z",
      raw_message: "U3ViamVjdDogSGVsbG8NCg0KSGkgdGhlcmUh",
    };
    mockGetMessage.mockResolvedValue(detail);
    mockParse.mockResolvedValue({
      messageId: "msg_123",
      sender: "alice@example.com",
      recipient: "bot@agents.e2a.dev",
      subject: "Hello",
      cc: [],
      textBody: "Hi there!",
    });

    await read("msg_123", undefined);

    expect(mockGetMessage).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_123");
    expect(mockParse).toHaveBeenCalledWith(detail);
    expect(mockStdout).toHaveBeenCalledWith("Message ID: msg_123\n");
    expect(mockStdout).toHaveBeenCalledWith("From: alice@example.com\n");
    expect(mockStdout).toHaveBeenCalledWith("Date: 2025-01-15T10:30:00Z\n");
    expect(mockStdout).toHaveBeenCalledWith("Subject: Hello\n");
    expect(mockStdout).toHaveBeenCalledWith("Hi there!\n");
  });

  it("shows 'unknown' when receivedAt is null", async () => {
    const detail = {
      message_id: "msg_456",
      from: "bob@example.com",
      to: "bot@agents.e2a.dev",
      subject: "Test",
      created_at: undefined,
      raw_message: "",
    };
    mockGetMessage.mockResolvedValue(detail);
    mockParse.mockResolvedValue({
      messageId: "msg_456",
      sender: "bob@example.com",
      recipient: "bot@agents.e2a.dev",
      subject: "Test",
      cc: [],
      textBody: "",
    });

    await read("msg_456", undefined);

    expect(mockStdout).toHaveBeenCalledWith("Date: unknown\n");
  });
});
