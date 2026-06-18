import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockDelete = vi.fn();
const mockUpdate = vi.fn();
const mockInfo = vi.fn();

// AutoPager stub: list() returns an object exposing toArray().
function pager(items: unknown[]) {
  return { toArray: vi.fn(async () => items) };
}

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    info: mockInfo,
    agents: {
      list: mockList,
      create: mockCreate,
      update: mockUpdate,
      delete: mockDelete,
    },
  })),
}));

vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "e2a_testkey",
    api_url: "https://e2a.dev",
    agent_email: "bot@agents.e2a.dev",
    shared_domain: "agents.e2a.dev",
  })),
  requireApiKey: vi.fn(() => "e2a_testkey"),
  saveConfig: vi.fn(),
}));

import { saveConfig } from "../config.js";
import {
  agentsList,
  agentsRegister,
  agentsDelete,
  agentsUpdate,
} from "../commands/agents.js";

describe("agentsList", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockList.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    vi.clearAllMocks();
  });

  it("lists agents with active marker", async () => {
    mockList.mockReturnValue(
      pager([
        { email: "bot@agents.e2a.dev", hitlEnabled: false },
        { email: "other@agents.e2a.dev", hitlEnabled: true },
      ]),
    );

    await agentsList(undefined);

    expect(mockStdout).toHaveBeenCalledWith("bot@agents.e2a.dev  no-hitl (active)\n");
    expect(mockStdout).toHaveBeenCalledWith("other@agents.e2a.dev  hitl\n");
  });

  it("shows message when no agents exist", async () => {
    mockList.mockReturnValue(pager([]));

    await agentsList(undefined);

    expect(mockStderr).toHaveBeenCalledWith(
      "No agents registered. Run: e2a agents register <slug>\n",
    );
  });
});

describe("agentsRegister", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockCreate.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no slug provided", async () => {
    await expect(agentsRegister(undefined)).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });

  it("registers agent and saves email", async () => {
    mockCreate.mockResolvedValue({
      id: "agent_123",
      email: "my-bot@agents.e2a.dev",
      domain: "agents.e2a.dev",
    });

    await agentsRegister("my-bot");

    expect(mockCreate).toHaveBeenCalledWith({
      slug: "my-bot",
      name: undefined,
    });
    expect(saveConfig).toHaveBeenCalledWith({ agent_email: "my-bot@agents.e2a.dev" });
    expect(mockStdout).toHaveBeenCalledWith("Registered: my-bot@agents.e2a.dev\n");
  });
});

describe("agentsDelete", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockDelete.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no email provided", async () => {
    await expect(agentsDelete(undefined)).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });

  it("deletes agent and confirms", async () => {
    mockDelete.mockResolvedValue(undefined);

    await agentsDelete("other@agents.e2a.dev");

    expect(mockDelete).toHaveBeenCalledWith("other@agents.e2a.dev");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: other@agents.e2a.dev\n");
  });

  it("expands slug to full email for shared domain", async () => {
    mockDelete.mockResolvedValue(undefined);

    await agentsDelete("my-bot");

    expect(mockDelete).toHaveBeenCalledWith("my-bot@agents.e2a.dev");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: my-bot@agents.e2a.dev\n");
  });

  it("preserves full email for custom domains", async () => {
    mockDelete.mockResolvedValue(undefined);

    await agentsDelete("support@custom.example.com");

    expect(mockDelete).toHaveBeenCalledWith("support@custom.example.com");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: support@custom.example.com\n");
  });

  it("clears config when deleting active agent", async () => {
    mockDelete.mockResolvedValue(undefined);

    await agentsDelete("bot@agents.e2a.dev");

    expect(saveConfig).toHaveBeenCalledWith({ agent_email: "" });
    expect(mockStderr).toHaveBeenCalledWith("Cleared active agent from config.\n");
  });
});

describe("agentsUpdate", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockUpdate.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("requires at least one flag", async () => {
    await expect(agentsUpdate("my-bot", {})).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(
      expect.stringContaining("No changes requested"),
    );
  });

  it("sends HITL enable + settings and prints the confirmed state", async () => {
    mockUpdate.mockResolvedValueOnce({
      email: "my-bot@agents.e2a.dev",
      hitlEnabled: true,
      hitlTtlSeconds: 3600,
      hitlExpirationAction: "approve",
    });

    await agentsUpdate("my-bot", {
      hitlEnabled: true,
      hitlTTLSeconds: 3600,
      hitlExpirationAction: "approve",
    });

    expect(mockUpdate).toHaveBeenCalledWith("my-bot@agents.e2a.dev", {
      hitlEnabled: true,
      hitlTtlSeconds: 3600,
      hitlExpirationAction: "approve",
    });
    const out = mockStdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("Updated: my-bot@agents.e2a.dev");
    expect(out).toContain("enabled");
    expect(out).toContain("auto-approve");
  });

  it("expands bare slug to shared-domain email", async () => {
    mockUpdate.mockResolvedValueOnce({
      email: "abc@agents.e2a.dev",
      hitlEnabled: false,
    });

    await agentsUpdate("abc", { hitlEnabled: false });

    const [address] = mockUpdate.mock.calls[0];
    expect(address).toBe("abc@agents.e2a.dev");
  });

  it("preserves full email for custom domains", async () => {
    mockUpdate.mockResolvedValueOnce({
      email: "support@acme.com",
      hitlEnabled: true,
    });

    await agentsUpdate("support@acme.com", { hitlEnabled: true });
    const [address] = mockUpdate.mock.calls[0];
    expect(address).toBe("support@acme.com");
  });
});
