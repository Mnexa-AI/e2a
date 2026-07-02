import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockGet = vi.fn();

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    agents: { list: mockList, create: mockCreate, get: mockGet },
  })),
  requireAgentEmail: vi.fn(() => "bot@agents.e2a.dev"),
}));

const AGENT = {
  id: "agt_1",
  email: "tether@agents.e2a.dev",
  name: "tether",
  domain: "agents.e2a.dev",
  domainVerified: true,
  createdAt: new Date("2026-07-01T10:00:00Z"),
};

describe("agents commands", () => {
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

  it("create passes email + name and prints the created address", async () => {
    mockCreate.mockResolvedValue(AGENT);
    const { agentsCreate } = await import("../commands/agents.js");
    await agentsCreate("tether@agents.e2a.dev", { name: "tether" });

    expect(mockCreate).toHaveBeenCalledWith({ email: "tether@agents.e2a.dev", name: "tether" });
    expect(mockStdout).toHaveBeenCalledWith("tether@agents.e2a.dev\n");
  });

  it("create without an email exits USAGE (2)", async () => {
    const { agentsCreate } = await import("../commands/agents.js");
    await expect(agentsCreate(undefined, {})).rejects.toThrow("process.exit");
    expect(mockExit).toHaveBeenCalledWith(2);
  });

  it("list prints TSV (email, name, verification)", async () => {
    mockList.mockReturnValue(
      (async function* () {
        yield AGENT;
      })(),
    );
    const { agentsList } = await import("../commands/agents.js");
    await agentsList({});

    expect(mockStdout).toHaveBeenCalledWith("tether@agents.e2a.dev\ttether\tverified\n");
  });

  it("get prints the agent summary", async () => {
    mockGet.mockResolvedValue(AGENT);
    const { agentsGet } = await import("../commands/agents.js");
    await agentsGet("tether@agents.e2a.dev", {});

    const output = mockStdout.mock.calls.map((c: unknown[]) => c[0]).join("");
    expect(output).toContain("email:    tether@agents.e2a.dev");
    expect(output).toContain("domain:   agents.e2a.dev (verified)");
    expect(output).toContain("created:  2026-07-01T10:00:00.000Z");
  });
});
