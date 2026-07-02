import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockList = vi.fn();
const mockGet = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({ messages: { list: mockList, get: mockGet } })),
  requireAgentEmail: vi.fn(() => "bot@agents.e2a.dev"),
}));

function summaries(...items: Array<Record<string, unknown>>) {
  return (async function* () {
    for (const item of items) yield item;
  })();
}

const M1 = {
  messageId: "msg_1",
  _from: "you@example.com",
  createdAt: new Date("2026-07-01T10:00:00Z"),
  subject: "re: status",
};
const M2 = {
  messageId: "msg_2",
  _from: "other@example.com",
  createdAt: new Date("2026-07-01T10:05:00Z"),
  subject: "re: status",
};

describe("messages commands", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it("lists messages as TSV, oldest first, with filters passed through", async () => {
    mockList.mockReturnValue(summaries(M1, M2));
    const { messagesList } = await import("../commands/messages.js");
    await messagesList({
      direction: "inbound",
      since: "2026-07-01T09:00:00Z",
      conversation: "conv-1",
    });

    expect(mockList).toHaveBeenCalledWith("bot@agents.e2a.dev", {
      sort: "asc",
      direction: "inbound",
      since: "2026-07-01T09:00:00Z",
      conversationId: "conv-1",
      limit: 100, // unbounded lists page at the server max, not its 50 default
    });
    expect(mockStdout).toHaveBeenCalledWith(
      "msg_1\tyou@example.com\t2026-07-01T10:00:00.000Z\n",
    );
    expect(mockStdout).toHaveBeenCalledWith(
      "msg_2\tother@example.com\t2026-07-01T10:05:00.000Z\n",
    );
  });

  it("emits NDJSON with --json", async () => {
    mockList.mockReturnValue(summaries(M1));
    const { messagesList } = await import("../commands/messages.js");
    await messagesList({ json: true });

    expect(mockStdout).toHaveBeenCalledWith(JSON.stringify(M1) + "\n");
  });

  it("stops after --limit items", async () => {
    mockList.mockReturnValue(summaries(M1, M2));
    const { messagesList } = await import("../commands/messages.js");
    await messagesList({ limit: "1" });

    const lines = mockStdout.mock.calls.map((c: unknown[]) => c[0]);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toContain("msg_1");
  });

  it("exits USAGE (2) on a bad --direction or --limit", async () => {
    const { messagesList } = await import("../commands/messages.js");
    await expect(messagesList({ direction: "sideways" })).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);

    mockExit.mockClear();
    await expect(messagesList({ limit: "zero" })).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });

  it("get --text prefers parsed text over raw body text", async () => {
    mockGet.mockResolvedValue({
      messageId: "msg_1",
      parsed: { text: "just the reply" },
      body: { text: "just the reply\n> quoted history" },
    });
    const { messagesGet } = await import("../commands/messages.js");
    await messagesGet("msg_1", { text: true });

    expect(mockGet).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_1");
    expect(mockStdout).toHaveBeenCalledWith("just the reply\n");
  });

  it("get --text falls back to body text, then empty", async () => {
    mockGet.mockResolvedValue({ messageId: "msg_1", body: { text: "raw body" } });
    const { messagesGet } = await import("../commands/messages.js");
    await messagesGet("msg_1", { text: true });
    expect(mockStdout).toHaveBeenCalledWith("raw body\n");

    mockGet.mockResolvedValue({ messageId: "msg_2" });
    await messagesGet("msg_2", { text: true });
    expect(mockStdout).toHaveBeenCalledWith("\n");
  });

  it("get emits full JSON without --text", async () => {
    const full = { messageId: "msg_1", subject: "s", conversationId: "conv-1" };
    mockGet.mockResolvedValue(full);
    const { messagesGet } = await import("../commands/messages.js");
    await messagesGet("msg_1", {});

    expect(mockStdout).toHaveBeenCalledWith(JSON.stringify(full) + "\n");
  });

  it("get exits USAGE (2) without a message id", async () => {
    const { messagesGet } = await import("../commands/messages.js");
    await expect(messagesGet(undefined, {})).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });

  it("get exits USAGE (2) when --text and --json are combined", async () => {
    const { messagesGet } = await import("../commands/messages.js");
    await expect(messagesGet("msg_1", { text: true, json: true })).rejects.toThrow(
      "process.exit",
    );
    expect(mockExit).toHaveBeenCalledWith(2);
  });
});
