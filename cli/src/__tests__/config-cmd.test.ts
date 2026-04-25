import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "e2a_testkey123456",
    api_url: "https://e2a.dev",
    agent_email: "bot@agents.e2a.dev",
  })),
  saveConfig: vi.fn(),
  requireApiKey: vi.fn(() => "e2a_testkey123456"),
}));

import { saveConfig } from "../config.js";

describe("config command", () => {
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

  it("lists all config values with truncated api_key", async () => {
    const { config } = await import("../commands/config.js");
    await config([]);

    const output = mockStdout.mock.calls.map((c: unknown[]) => c[0]).join("");
    expect(output).toContain("api_key=e2a_test…");
    expect(output).toContain("api_url=https://e2a.dev");
    expect(output).toContain("agent_email=bot@agents.e2a.dev");
  });

  it("gets a specific key", async () => {
    const { config } = await import("../commands/config.js");
    await config(["get", "agent_email"]);

    expect(mockStdout).toHaveBeenCalledWith("bot@agents.e2a.dev\n");
  });

  it("sets a key", async () => {
    const { config } = await import("../commands/config.js");
    await config(["set", "agent_email", "new@agents.e2a.dev"]);

    expect(saveConfig).toHaveBeenCalledWith({ agent_email: "new@agents.e2a.dev" });
    expect(mockStdout).toHaveBeenCalledWith("agent_email=new@agents.e2a.dev\n");
  });

  it("rejects unknown keys on get", async () => {
    const { config } = await import("../commands/config.js");
    await expect(config(["get", "bad_key"])).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Unknown key"));
  });

  it("rejects unknown keys on set", async () => {
    const { config } = await import("../commands/config.js");
    await expect(config(["set", "bad_key", "val"])).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Unknown key"));
  });

  it("treats bare valid key as get shorthand", async () => {
    const { config } = await import("../commands/config.js");
    await config(["api_url"]);

    expect(mockStdout).toHaveBeenCalledWith("https://e2a.dev\n");
  });

  it("exits with usage on unknown subcommand", async () => {
    const { config } = await import("../commands/config.js");
    await expect(config(["foo"])).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });
});
