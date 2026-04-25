import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockListAgents = vi.fn();
const mockRegisterAgent = vi.fn();
const mockDeleteAgent = vi.fn();
const mockUpdateAgent = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agentEmail: "bot@agents.e2a.dev",
    updateAgent: mockUpdateAgent,
    api: {
      listAgents: mockListAgents,
      registerAgent: mockRegisterAgent,
      deleteAgent: mockDeleteAgent,
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
    mockListAgents.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    vi.clearAllMocks();
  });

  it("lists agents with active marker", async () => {
    mockListAgents.mockResolvedValue({
      agents: [
        { email: "bot@agents.e2a.dev", agent_mode: "local" },
        { email: "other@agents.e2a.dev", agent_mode: "cloud" },
      ],
    });

    await agentsList(undefined);

    expect(mockStdout).toHaveBeenCalledWith("bot@agents.e2a.dev  local (active)\n");
    expect(mockStdout).toHaveBeenCalledWith("other@agents.e2a.dev  cloud\n");
  });

  it("shows message when no agents exist", async () => {
    mockListAgents.mockResolvedValue({ agents: [] });

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
    mockRegisterAgent.mockReset();
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
    mockRegisterAgent.mockResolvedValue({
      id: "agent_123",
      email: "my-bot@agents.e2a.dev",
      domain: "agents.e2a.dev",
    });

    await agentsRegister("my-bot");

    expect(mockRegisterAgent).toHaveBeenCalledWith({
      slug: "my-bot",
      agent_mode: "local",
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
    mockDeleteAgent.mockReset();
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
    mockDeleteAgent.mockResolvedValue(undefined);

    await agentsDelete("other@agents.e2a.dev");

    expect(mockDeleteAgent).toHaveBeenCalledWith("other@agents.e2a.dev");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: other@agents.e2a.dev\n");
  });

  it("expands slug to full email for shared domain", async () => {
    mockDeleteAgent.mockResolvedValue(undefined);

    await agentsDelete("my-bot");

    expect(mockDeleteAgent).toHaveBeenCalledWith("my-bot@agents.e2a.dev");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: my-bot@agents.e2a.dev\n");
  });

  it("preserves full email for custom domains", async () => {
    mockDeleteAgent.mockResolvedValue(undefined);

    await agentsDelete("support@custom.example.com");

    expect(mockDeleteAgent).toHaveBeenCalledWith("support@custom.example.com");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: support@custom.example.com\n");
  });

  it("clears config when deleting active agent", async () => {
    mockDeleteAgent.mockResolvedValue(undefined);

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
    mockUpdateAgent.mockReset();
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
    mockUpdateAgent.mockResolvedValueOnce({
      email: "my-bot@agents.e2a.dev",
      agent_mode: "local",
      hitl_enabled: true,
      hitl_ttl_seconds: 3600,
      hitl_expiration_action: "approve",
    });

    await agentsUpdate("my-bot", {
      hitlEnabled: true,
      hitlTTLSeconds: 3600,
      hitlExpirationAction: "approve",
    });

    expect(mockUpdateAgent).toHaveBeenCalledWith(
      {
        hitl_enabled: true,
        hitl_ttl_seconds: 3600,
        hitl_expiration_action: "approve",
      },
      { agentEmail: "my-bot@agents.e2a.dev" },
    );
    const out = mockStdout.mock.calls.map((c: unknown[]) => String(c[0])).join("");
    expect(out).toContain("Updated: my-bot@agents.e2a.dev");
    expect(out).toContain("enabled");
    expect(out).toContain("auto-approve");
  });

  it("expands bare slug to shared-domain email", async () => {
    mockUpdateAgent.mockResolvedValueOnce({
      email: "abc@agents.e2a.dev",
      hitl_enabled: false,
    });

    await agentsUpdate("abc", { hitlEnabled: false });

    const [, ctx] = mockUpdateAgent.mock.calls[0];
    expect(ctx).toEqual({ agentEmail: "abc@agents.e2a.dev" });
  });

  it("preserves full email for custom domains", async () => {
    mockUpdateAgent.mockResolvedValueOnce({
      email: "support@acme.com",
      hitl_enabled: true,
    });

    await agentsUpdate("support@acme.com", { hitlEnabled: true });
    const [, ctx] = mockUpdateAgent.mock.calls[0];
    expect(ctx).toEqual({ agentEmail: "support@acme.com" });
  });
});
