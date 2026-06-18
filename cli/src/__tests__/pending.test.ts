import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the namespaced messages resource the CLI now calls. Pending drafts
// surface as outbound messages with status="pending_approval".
const mockList = vi.fn();
const mockGet = vi.fn();
const mockApprove = vi.fn();
const mockReject = vi.fn();

function pager(items: unknown[]) {
  return { toArray: vi.fn(async () => items) };
}

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    messages: {
      list: mockList,
      get: mockGet,
      approve: mockApprove,
      reject: mockReject,
    },
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
    mockList.mockReset();
  });
  afterEach(() => {
    stdout.mockRestore();
    vi.clearAllMocks();
  });

  it("prints a line per pending message", async () => {
    mockList.mockReturnValue(
      pager([
        {
          messageId: "msg_abc",
          _from: "bot@x.com",
          subject: "hello",
          to: ["alice@example.com"],
          status: "pending_approval",
        },
        // A sent outbound message should be filtered out.
        {
          messageId: "msg_sent",
          _from: "bot@x.com",
          subject: "already sent",
          to: ["bob@example.com"],
          status: "sent",
        },
      ]),
    );

    await pendingList();
    expect(mockList).toHaveBeenCalledWith("bot@agents.e2a.dev", { direction: "outbound" });
    const output = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(output).toContain("msg_abc");
    expect(output).toContain("bot@x.com");
    expect(output).toContain("alice@example.com");
    expect(output).toContain("hello");
    expect(output).not.toContain("msg_sent");
  });

  it('shows "no pending" message when list is empty', async () => {
    mockList.mockReturnValue(pager([]));
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
    mockGet.mockReset();
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
    mockGet.mockResolvedValueOnce({
      messageId: "msg_x",
      _from: "bot@x.com",
      subject: "hello",
      to: ["alice@example.com"],
      cc: ["carol@example.com"],
      status: "pending_approval",
      body: { text: "hi there", html: "" },
      parsed: { text: "hi there", truncated: false },
    });

    await pendingShow("msg_x");
    expect(mockGet).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x");
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
    mockGet.mockReset();
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

  it("approve-as-is fetches the message to confirm it is held, then approves", async () => {
    mockGet.mockResolvedValueOnce({
      messageId: "msg_x",
      status: "pending_approval",
      subject: "hi",
      to: ["alice@example.com"],
    });
    mockApprove.mockResolvedValueOnce({
      status: "sent",
      messageId: "msg_x",
      providerMessageId: "<ses-id@amazonses.com>",
      method: "smtp",
      edited: false,
    });

    await pendingApprove("msg_x", {});

    expect(mockGet).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x");
    expect(mockApprove).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", {}, undefined);
    const out = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("Approved: msg_x");
    expect(out).toContain("<ses-id@amazonses.com>");
    expect(out).not.toContain("with edits");
  });

  it('annotates "with edits" when server reports edited', async () => {
    mockGet.mockResolvedValueOnce({
      messageId: "msg_x",
      status: "pending_approval",
      subject: "hi",
      to: ["alice@example.com"],
    });
    mockApprove.mockResolvedValueOnce({
      status: "sent",
      messageId: "msg_x",
      providerMessageId: "<ses@amazonses.com>",
      method: "smtp",
      edited: true,
    });

    await pendingApprove("msg_x", {});
    const out = stdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("(with edits)");
  });

  it("refuses to approve when the row has already transitioned", async () => {
    mockGet.mockResolvedValueOnce({
      messageId: "msg_x",
      status: "sent",
      subject: "hi",
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

  it("forwards the reason to the SDK for the active agent", async () => {
    mockReject.mockResolvedValueOnce({
      status: "rejected",
      messageId: "msg_x",
      rejectionReason: "nope",
    });

    await pendingReject("msg_x", "nope");
    expect(mockReject).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", { reason: "nope" });
    expect(stdout).toHaveBeenCalledWith("Rejected: msg_x\n");
  });

  it("passes an empty reason when omitted", async () => {
    mockReject.mockResolvedValueOnce({ status: "rejected", messageId: "msg_x" });
    await pendingReject("msg_x", undefined);
    expect(mockReject).toHaveBeenCalledWith("bot@agents.e2a.dev", "msg_x", { reason: "" });
  });
});
