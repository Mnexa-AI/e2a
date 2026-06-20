import { describe, expect, it, vi } from "vitest";
import { ConfigError, loadConfig } from "../src/config.js";

describe("loadConfig", () => {
  it("reads required and optional vars (canonical E2A_URL)", () => {
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_URL: "https://api.example.com",
    });
    // E2A_AGENT_EMAIL was removed (§9a): an agent-scoped credential IS its
    // agent (resolved server-side / by the session prefetch), and
    // account-scoped callers pass `email` per tool. loadConfig no longer
    // reads any default-agent env var, so agentEmail is always undefined.
    expect(cfg).toEqual({
      apiKey: "key_123",
      baseUrl: "https://api.example.com",
      agentEmail: undefined,
    });
  });

  it("treats empty optional vars as undefined", () => {
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_URL: "",
      E2A_BASE_URL: "",
    });
    expect(cfg.baseUrl).toBeUndefined();
    // No default-agent env is read anymore — agentEmail is never populated.
    expect(cfg.agentEmail).toBeUndefined();
  });

  it("throws ConfigError when API key is missing", () => {
    expect(() => loadConfig({})).toThrowError(ConfigError);
    expect(() => loadConfig({})).toThrow(/E2A_API_KEY/);
  });

  // Dual-read fallback for the legacy E2A_BASE_URL name. PR #82 shipped
  // the MCP server with E2A_BASE_URL; the canonical name is now E2A_URL
  // (matches the CLI). Both must work indefinitely so existing user
  // deployments (Claude Desktop / Codex / ADK env blocks pinned against
  // the public MCP catalog manifest) don't break.

  it("falls back to E2A_BASE_URL when E2A_URL is unset", () => {
    const warn = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_BASE_URL: "https://legacy.example.com",
    });
    expect(cfg.baseUrl).toBe("https://legacy.example.com");
    warn.mockRestore();
  });

  it("prefers canonical E2A_URL when both are set", () => {
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_URL: "https://canonical.example.com",
      E2A_BASE_URL: "https://legacy.example.com",
    });
    expect(cfg.baseUrl).toBe("https://canonical.example.com");
  });
});
