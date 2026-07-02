import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockSend = vi.fn();
const mockReply = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({ messages: { send: mockSend, reply: mockReply } })),
  requireAgentEmail: vi.fn((override?: string) => override || "bot@agents.e2a.dev"),
}));

const mockReadFileSync = vi.fn();
vi.mock("node:fs", () => ({
  readFileSync: (...args: unknown[]) => mockReadFileSync(...args),
}));

describe("send/reply commands", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("sends a plain-text message and prints the message id", async () => {
    mockSend.mockResolvedValue({ messageId: "msg_abc", status: "sent" });
    const { send } = await import("../commands/send.js");
    await send({ to: ["you@example.com"], subject: "hi", body: "hello" });

    expect(mockSend).toHaveBeenCalledWith("bot@agents.e2a.dev", {
      to: ["you@example.com"],
      subject: "hi",
      body: "hello",
    });
    expect(mockStdout).toHaveBeenCalledWith("msg_abc\n");
    expect(mockExit).not.toHaveBeenCalled();
  });

  it("exits HELD (3) with a warning when the send is pending_review", async () => {
    mockSend.mockResolvedValue({ messageId: "msg_held", status: "pending_review" });
    const { send } = await import("../commands/send.js");

    await expect(
      send({ to: ["you@example.com"], subject: "hi", body: "hello" }),
    ).rejects.toThrow("process.exit");

    // The id is still printed (the message exists, parked in the queue) …
    expect(mockStdout).toHaveBeenCalledWith("msg_held\n");
    // … but the held contract fires: warning + exit 3.
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("pending_review"));
    expect(mockExit).toHaveBeenCalledWith(3);
  });

  it("derives a text fallback from --html-file when no --body is given", async () => {
    mockReadFileSync.mockReturnValue("<p>Status: <b>green</b></p>");
    mockSend.mockResolvedValue({ messageId: "msg_h", status: "sent" });
    const { send } = await import("../commands/send.js");
    await send({ to: ["you@example.com"], subject: "s", htmlFile: "/tmp/u.html" });

    const call = mockSend.mock.calls[0][1];
    expect(call.htmlBody).toBe("<p>Status: <b>green</b></p>");
    expect(call.body).toBe("Status: green");
  });

  it("passes conversation id through", async () => {
    mockSend.mockResolvedValue({ messageId: "msg_c", status: "sent" });
    const { send } = await import("../commands/send.js");
    await send({
      to: ["you@example.com"],
      subject: "s",
      body: "b",
      conversationId: "conv-1",
    });

    expect(mockSend.mock.calls[0][1].conversationId).toBe("conv-1");
  });

  it("exits USAGE (2) when --to or --subject is missing", async () => {
    const { send } = await import("../commands/send.js");
    await expect(send({ to: [], subject: "s", body: "b" })).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });

  it("exits USAGE (2) when no body source is given", async () => {
    const { send } = await import("../commands/send.js");
    await expect(send({ to: ["a@b.c"], subject: "s" })).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });

  it("exits USAGE (2) when --html-file is unreadable", async () => {
    mockReadFileSync.mockImplementation(() => {
      throw new Error("ENOENT");
    });
    const { send } = await import("../commands/send.js");
    await expect(
      send({ to: ["a@b.c"], subject: "s", htmlFile: "/nope.html" }),
    ).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("/nope.html"));
  });

  it("replies in-thread and honors the held contract", async () => {
    mockReply.mockResolvedValue({ messageId: "msg_r", status: "pending_review" });
    const { reply } = await import("../commands/send.js");

    await expect(reply("msg_orig", { body: "answer" })).rejects.toThrow("process.exit");
    expect(mockReply).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_orig", {
      body: "answer",
    });
    expect(mockExit).toHaveBeenCalledWith(3);
  });

  it("exits USAGE (2) when reply is missing the message id", async () => {
    const { reply } = await import("../commands/send.js");
    await expect(reply(undefined, { body: "answer" })).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });
});
