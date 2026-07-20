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

  describe("env var interference warnings", () => {
    // Clean up env before each test
    const originalEnv = process.env;

    beforeEach(() => {
      process.env = { ...originalEnv };
    });

    afterEach(() => {
      process.env = originalEnv;
    });

    it("warns when api_url is set and E2A_URL env var exists (NOT PERSISTED)", async () => {
      process.env.E2A_URL = "https://example.com";
      const { config } = await import("../commands/config.js");
      await config(["set", "api_url", "http://localhost:8080"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("api_url=http://localhost:8080\n");

      // Check that stderr has the warning about E2A_URL blocking writes
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).toContain("api_url was not saved");
      expect(stderrCalls).toContain("E2A_URL");
      expect(stderrCalls).toContain("blocks writes");
    });

    it("warns when shared_domain is set and E2A_SHARED_DOMAIN env var exists (NOT PERSISTED)", async () => {
      process.env.E2A_SHARED_DOMAIN = "agents.example.com";
      const { config } = await import("../commands/config.js");
      await config(["set", "shared_domain", "agents.custom.com"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("shared_domain=agents.custom.com\n");

      // Check that stderr has the warning about E2A_SHARED_DOMAIN blocking writes
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).toContain("shared_domain was not saved");
      expect(stderrCalls).toContain("E2A_SHARED_DOMAIN");
      expect(stderrCalls).toContain("blocks writes");
    });

    it("warns when api_key is set and E2A_API_KEY env var exists (PERSISTED BUT SHADOWED)", async () => {
      process.env.E2A_API_KEY = "e2a_envkey";
      const { config } = await import("../commands/config.js");
      await config(["set", "api_key", "e2a_newkey123"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("api_key=e2a_newkey123\n");

      // Check that stderr has the warning about E2A_API_KEY shadowing
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).toContain("api_key was saved");
      expect(stderrCalls).toContain("E2A_API_KEY");
      expect(stderrCalls).toContain("take precedence");
    });

    it("warns when agent_email is set and E2A_AGENT_EMAIL env var exists (PERSISTED BUT SHADOWED)", async () => {
      process.env.E2A_AGENT_EMAIL = "env@agents.e2a.dev";
      const { config } = await import("../commands/config.js");
      await config(["set", "agent_email", "new@agents.e2a.dev"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("agent_email=new@agents.e2a.dev\n");

      // Check that stderr has the warning about E2A_AGENT_EMAIL shadowing
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).toContain("agent_email was saved");
      expect(stderrCalls).toContain("E2A_AGENT_EMAIL");
      expect(stderrCalls).toContain("take precedence");
    });

    it("does not warn when setting config with no env vars (clean case)", async () => {
      // Ensure env vars are not set
      delete process.env.E2A_URL;
      delete process.env.E2A_SHARED_DOMAIN;
      delete process.env.E2A_API_KEY;
      delete process.env.E2A_AGENT_EMAIL;

      const { config } = await import("../commands/config.js");
      await config(["set", "agent_email", "clean@agents.e2a.dev"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("agent_email=clean@agents.e2a.dev\n");

      // Check that stderr has NO warnings (only calls from the mock setup, not from warnIfEnvVarInterferes)
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).not.toContain("was not saved");
      expect(stderrCalls).not.toContain("take precedence");
    });

    it("does not warn when setting key_scope (no env interference)", async () => {
      const { config } = await import("../commands/config.js");
      await config(["set", "key_scope", "account"]);

      // Check that stdout has the success message
      expect(mockStdout).toHaveBeenCalledWith("key_scope=account\n");

      // Check that stderr has no interference warnings
      const stderrCalls = mockStderr.mock.calls.map((c: unknown[]) => c[0]).join("");
      expect(stderrCalls).not.toContain("was not saved");
      expect(stderrCalls).not.toContain("take precedence");
    });
  });
});
