import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the high-level SDK methods the CLI now calls. Each test installs a
// fresh resolved value so we can assert exactly what the CLI sent and
// renders what the server would return.
const mockListPending = vi.fn();
const mockGetPending = vi.fn();
const mockApprove = vi.fn();
const mockReject = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agentEmail: "bot@agents.e2a.dev",
    listPendingMessages: mockListPending,
    getPendingMessage: mockGetPending,
    approveMessage: mockApprove,
    rejectMessage: mockReject,
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

import {
  pendingList,
  pendingShow,
  pendingApprove,
  pendingReject,
} from "../commands/pending.js";

describe("pendingList", () => {
  let stdout: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    stdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockListPending.mockReset();
  });
  afterEach(() => {
    stdout.mockRestore();
    vi.clearAllMocks();
  });

  it("prints a line per pending message", async () => {
    mockListPending.mockResolvedValueOnce({
      messages: [
        {
          id: "msg_abc",
          agent_id: "bot@x.com",
          subject: "hello",
          type: "send",
          to: ["alice@example.com"],
          status: "pending_approval",
          approval_expires_at: new Date(Date.now() + 60 * 60_000).toISOString(),
          created_at: new Date().toISOString(),
        },
      ],
    });

    await pendingList();
    expect(mockListPending).toHaveBeenCalled();
    const output = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(output).toContain("msg_abc");
    expect(output).toContain("bot@x.com");
    expect(output).toContain("alice@example.com");
    expect(output).toContain("hello");
  });

  it('shows "no pending" message when list is empty', async () => {
    mockListPending.mockResolvedValueOnce({ messages: [] });
    await pendingList();
    expect(stdout).toHaveBeenCalledWith("No messages pending approval.\n");
  });
});

describe("pendingShow", () => {
  let stdout: ReturnType<typeof vi.spyOn>;
  let stderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    stdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    stderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockGetPending.mockReset();
  });
  afterEach(() => {
    stdout.mockRestore();
    stderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("requires an id", async () => {
    await expect(pendingShow(undefined)).rejects.toThrow("process.exit");
    expect(stderr).toHaveBeenCalledWith(expect.stringContaining("Usage"));
  });

  it("prints all headers + body for a pending message", async () => {
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@x.com",
      subject: "hello",
      type: "send",
      to: ["alice@example.com"],
      cc: ["carol@example.com"],
      status: "pending_approval",
      body_text: "hi there",
      created_at: new Date().toISOString(),
    });

    await pendingShow("msg_x");
    expect(mockGetPending).toHaveBeenCalledWith("msg_x");
    const output = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(output).toContain("ID:");
    expect(output).toContain("msg_x");
    expect(output).toContain("alice@example.com");
    expect(output).toContain("carol@example.com");
    expect(output).toContain("hi there");
  });
});

describe("pendingApprove", () => {
  let stdout: ReturnType<typeof vi.spyOn>;
  let stderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    stdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    stderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockApprove.mockReset();
    mockGetPending.mockReset();
  });
  afterEach(() => {
    stdout.mockRestore();
    stderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("requires an id", async () => {
    await expect(pendingApprove(undefined, {})).rejects.toThrow("process.exit");
    expect(stderr).toHaveBeenCalledWith(expect.stringContaining("Usage"));
  });

  it("approve-as-is fetches the pending detail for agent_id, then approves", async () => {
    // CLI now always fetches the message first to discover the owning
    // agent (needed for the agent-scoped /agents/{email}/messages/...
    // endpoint). Approve-as-is still sends an empty override object.
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@agents.e2a.dev",
      status: "pending_approval",
      subject: "hi",
      type: "send",
      to: ["alice@example.com"],
    });
    mockApprove.mockResolvedValueOnce({
      status: "sent",
      message_id: "msg_x",
      provider_message_id: "<ses-id@amazonses.com>",
      method: "smtp",
      edited: false,
    });

    await pendingApprove("msg_x", {});

    expect(mockGetPending).toHaveBeenCalledWith("msg_x");
    expect(mockApprove).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", {});
    const out = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("Approved: msg_x");
    expect(out).toContain("<ses-id@amazonses.com>");
    expect(out).not.toContain("with edits");
  });

  it('annotates "with edits" when server reports edited', async () => {
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@agents.e2a.dev",
      status: "pending_approval",
      subject: "hi",
      type: "send",
      to: ["alice@example.com"],
    });
    mockApprove.mockResolvedValueOnce({
      status: "sent",
      message_id: "msg_x",
      provider_message_id: "<ses@amazonses.com>",
      method: "smtp",
      edited: true,
    });

    await pendingApprove("msg_x", {});
    const out = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("(with edits)");
  });

  it("refuses to approve when the row has already transitioned", async () => {
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@agents.e2a.dev",
      status: "sent",
      subject: "hi",
      type: "send",
      to: ["alice@example.com"],
    });

    await expect(pendingApprove("msg_x", {})).rejects.toThrow("process.exit");
    expect(mockApprove).not.toHaveBeenCalled();
    expect(stderr).toHaveBeenCalledWith(expect.stringContaining("Cannot approve"));
  });
});

describe("pendingReject", () => {
  let stdout: ReturnType<typeof vi.spyOn>;
  let stderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    stdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    stderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockReject.mockReset();
    mockGetPending.mockReset();
  });
  afterEach(() => {
    stdout.mockRestore();
    stderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("requires an id", async () => {
    await expect(pendingReject(undefined, "nope")).rejects.toThrow("process.exit");
    expect(stderr).toHaveBeenCalledWith(expect.stringContaining("Usage"));
  });

  it("looks up the owning agent then forwards the reason to the SDK", async () => {
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@agents.e2a.dev",
      status: "pending_approval",
      subject: "hi",
      type: "send",
      to: ["alice@example.com"],
    });
    mockReject.mockResolvedValueOnce({
      status: "rejected",
      message_id: "msg_x",
      rejection_reason: "nope",
    });

    await pendingReject("msg_x", "nope");
    expect(mockGetPending).toHaveBeenCalledWith("msg_x");
    expect(mockReject).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", "nope");
    expect(stdout).toHaveBeenCalledWith("Rejected: msg_x\n");
  });

  it("passes an empty reason when omitted", async () => {
    mockGetPending.mockResolvedValueOnce({
      id: "msg_x",
      agent_id: "bot@agents.e2a.dev",
      status: "pending_approval",
      subject: "hi",
      type: "send",
      to: ["alice@example.com"],
    });
    mockReject.mockResolvedValueOnce({ status: "rejected", message_id: "msg_x" });
    await pendingReject("msg_x", undefined);
    expect(mockReject).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", "");
  });
});
