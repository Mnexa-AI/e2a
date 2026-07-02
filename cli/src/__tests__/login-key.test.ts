import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockAccountGet = vi.fn();

vi.mock("@e2a/sdk/v1", () => ({
  // `new E2AClient(...)` requires a constructible mock (arrow fns are not).
  E2AClient: vi.fn(function () {
    return { account: { get: mockAccountGet } };
  }),
}));

const mockSaveConfig = vi.fn();
vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "",
    api_url: "https://e2a.dev",
    agent_email: "",
    shared_domain: "agents.e2a.dev",
  })),
  saveConfig: (...args: unknown[]) => mockSaveConfig(...args),
}));

const BASE_CONFIG = {
  api_key: "",
  api_url: "https://e2a.dev",
  agent_email: "",
  shared_domain: "agents.e2a.dev",
};

describe("login --with-key", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;
  const hadKey = process.env.E2A_API_KEY;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    delete process.env.E2A_API_KEY;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
    if (hadKey) process.env.E2A_API_KEY = hadKey;
    else delete process.env.E2A_API_KEY;
  });

  it("validates the key via /v1/account and saves key + scope + bound agent", async () => {
    mockAccountGet.mockResolvedValue({
      scope: "agent",
      agentAddress: "tether@agents.e2a.dev",
      user: { id: "usr_1", email: "o@x.com" },
    });
    const { loginWithKey } = await import("../commands/login.js");
    await loginWithKey(BASE_CONFIG, "e2a_agt_secret");

    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_agt_secret",
      key_scope: "agent",
      agent_email: "tether@agents.e2a.dev",
    });
    const output = mockStdout.mock.calls.map((c: unknown[]) => c[0]).join("");
    expect(output).toContain("agent-scoped key");
    expect(output).toContain("Bound agent: tether@agents.e2a.dev");
  });

  it("account-scoped keys save without overwriting agent_email", async () => {
    mockAccountGet.mockResolvedValue({
      scope: "account",
      user: { id: "usr_1", email: "o@x.com" },
    });
    const { loginWithKey } = await import("../commands/login.js");
    await loginWithKey(BASE_CONFIG, "e2a_acct_secret");

    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_acct_secret",
      key_scope: "account",
    });
  });

  it("falls back to $E2A_API_KEY when no key argument or stdin is given", async () => {
    const hadTTY = process.stdin.isTTY;
    Object.defineProperty(process.stdin, "isTTY", { value: true, configurable: true });
    process.env.E2A_API_KEY = "e2a_agt_fromenv";
    try {
      mockAccountGet.mockResolvedValue({
        scope: "agent",
        agentAddress: "t@agents.e2a.dev",
        user: { id: "u", email: "o@x.com" },
      });
      const { loginWithKey } = await import("../commands/login.js");
      await loginWithKey(BASE_CONFIG);

      expect(mockSaveConfig.mock.calls[0][0].api_key).toBe("e2a_agt_fromenv");
    } finally {
      Object.defineProperty(process.stdin, "isTTY", { value: hadTTY, configurable: true });
    }
  });

  it("piped stdin OUTRANKS a stale $E2A_API_KEY (key rotation must rotate)", async () => {
    process.env.E2A_API_KEY = "e2a_agt_staleenv";
    const realStdin = Object.getOwnPropertyDescriptor(process, "stdin");
    Object.defineProperty(process, "stdin", {
      configurable: true,
      value: {
        isTTY: false,
        [Symbol.asyncIterator]: async function* () {
          yield "e2a_agt_fromstdin\n";
        },
      },
    });
    try {
      mockAccountGet.mockResolvedValue({
        scope: "agent",
        agentAddress: "t@agents.e2a.dev",
        user: { id: "u", email: "o@x.com" },
      });
      const { loginWithKey } = await import("../commands/login.js");
      await loginWithKey(BASE_CONFIG);

      expect(mockSaveConfig.mock.calls[0][0].api_key).toBe("e2a_agt_fromstdin");
    } finally {
      if (realStdin) Object.defineProperty(process, "stdin", realStdin);
    }
  });

  it("exits USAGE (2) with no key source on a TTY", async () => {
    const hadTTY = process.stdin.isTTY;
    Object.defineProperty(process.stdin, "isTTY", { value: true, configurable: true });
    try {
      const { loginWithKey } = await import("../commands/login.js");
      await expect(loginWithKey(BASE_CONFIG)).rejects.toThrow("process.exit");
      expect(mockExit).toHaveBeenCalledWith(2);
    } finally {
      Object.defineProperty(process.stdin, "isTTY", { value: hadTTY, configurable: true });
    }
  });
});
