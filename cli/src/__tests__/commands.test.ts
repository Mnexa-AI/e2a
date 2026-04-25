import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockSend = vi.fn();
const mockReply = vi.fn();
const mockRegisterAgent = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agentEmail: "bot@agents.e2a.dev",
    send: mockSend,
    reply: mockReply,
    api: {
      registerAgent: mockRegisterAgent,
    },
  })),
}));

vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "e2a_testkey",
    api_url: "https://e2a.dev",
    agent_email: "bot@agents.e2a.dev",
  })),
  requireApiKey: vi.fn(() => "e2a_testkey"),
  saveConfig: vi.fn(),
}));

import { saveConfig } from "../config.js";

describe("send", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockSend.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no visible recipients", async () => {
    const { send } = await import("../commands/send.js");
    await expect(send([], "Subject", "Body", {})).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });

  it("sends email via SDK", async () => {
    mockSend.mockResolvedValue({ status: "sent", message_id: "msg_sent_1" });

    const { send } = await import("../commands/send.js");
    await send(["user@example.com"], "Hello", "Body text", {});

    expect(mockSend).toHaveBeenCalledWith(
      ["user@example.com"], "Hello", "Body text",
      { htmlBody: undefined, cc: undefined, bcc: undefined },
    );
    expect(mockStdout).toHaveBeenCalledWith("Sent: msg_sent_1\n");
  });

  it("allows CC-only send (no --to)", async () => {
    mockSend.mockResolvedValue({ status: "sent", message_id: "msg_cc_1" });

    const { send } = await import("../commands/send.js");
    await send([], "Hello", "Body", { cc: ["alice@example.com"] });

    expect(mockSend).toHaveBeenCalledWith(
      [], "Hello", "Body",
      { htmlBody: undefined, cc: ["alice@example.com"], bcc: undefined },
    );
    expect(mockStdout).toHaveBeenCalledWith("Sent: msg_cc_1\n");
  });
});

describe("reply", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockReply.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when messageId is missing", async () => {
    const { reply } = await import("../commands/reply.js");
    await expect(reply(undefined, "hello", {})).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });

  it("exits when body is empty", async () => {
    const { reply } = await import("../commands/reply.js");
    await expect(reply("msg_123", "", {})).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith("--body is required\n");
  });

  it("sends reply via SDK", async () => {
    mockReply.mockResolvedValue({ status: "sent", message_id: "msg_reply_1" });

    const { reply } = await import("../commands/reply.js");
    await reply("msg_123", "Thanks!", {});

    expect(mockReply).toHaveBeenCalledWith("msg_123", "Thanks!", {
      htmlBody: undefined, replyAll: undefined, cc: undefined, bcc: undefined,
    });
    expect(mockStdout).toHaveBeenCalledWith("Sent: msg_reply_1\n");
  });
});

describe("register", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockRegisterAgent.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("registers agent and saves email to config", async () => {
    mockRegisterAgent.mockResolvedValue({
      id: "agent_123",
      email: "my-bot@agents.e2a.dev",
      domain: "agents.e2a.dev",
    });

    const { agentsRegister } = await import("../commands/agents.js");
    await agentsRegister("my-bot");

    expect(mockRegisterAgent).toHaveBeenCalledWith({
      slug: "my-bot",
      agent_mode: "local",
    });
    expect(saveConfig).toHaveBeenCalledWith({ agent_email: "my-bot@agents.e2a.dev" });
    expect(mockStdout).toHaveBeenCalledWith("Registered: my-bot@agents.e2a.dev\n");
  });
});
