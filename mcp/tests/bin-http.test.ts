import { describe, expect, it, vi } from "vitest";
import { ConfigError, loadConfig } from "../src/bin/http.js";

describe("bin/http loadConfig", () => {
  it("returns defaults when env is empty", () => {
    const cfg = loadConfig({});
    expect(cfg).toEqual({
      port: 3000,
      baseUrl: "https://e2a.dev",
      allowedHosts: ["mcp.e2a.dev"],
      sessionIdleMs: 5 * 60_000,
      maxSessions: 500,
      trustProxy: "loopback",
    });
  });

  it("parses valid values (canonical E2A_URL)", () => {
    const cfg = loadConfig({
      PORT: "8080",
      E2A_URL: "https://staging.e2a.dev",
      MCP_ALLOWED_HOSTS: "mcp.e2a.dev,mcp-staging.e2a.dev",
      MCP_SESSION_IDLE_MS: "60000",
      MCP_MAX_SESSIONS: "100",
    });
    expect(cfg).toEqual({
      port: 8080,
      baseUrl: "https://staging.e2a.dev",
      allowedHosts: ["mcp.e2a.dev", "mcp-staging.e2a.dev"],
      sessionIdleMs: 60_000,
      maxSessions: 100,
      trustProxy: "loopback",
    });
  });

  it("falls back to E2A_BASE_URL when E2A_URL is unset", () => {
    const warn = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const cfg = loadConfig({ E2A_BASE_URL: "https://legacy.example.com" });
    expect(cfg.baseUrl).toBe("https://legacy.example.com");
    warn.mockRestore();
  });

  it("prefers canonical E2A_URL when both are set", () => {
    const cfg = loadConfig({
      E2A_URL: "https://canonical.example.com",
      E2A_BASE_URL: "https://legacy.example.com",
    });
    expect(cfg.baseUrl).toBe("https://canonical.example.com");
  });

  it("rejects non-numeric PORT", () => {
    expect(() => loadConfig({ PORT: "abc" })).toThrowError(ConfigError);
    expect(() => loadConfig({ PORT: "abc" })).toThrow(/PORT/);
  });

  it("rejects negative PORT", () => {
    expect(() => loadConfig({ PORT: "-1" })).toThrowError(ConfigError);
  });

  it("rejects port over 65535", () => {
    expect(() => loadConfig({ PORT: "70000" })).toThrowError(ConfigError);
  });

  it("rejects MCP_MAX_SESSIONS=0", () => {
    expect(() => loadConfig({ MCP_MAX_SESSIONS: "0" })).toThrowError(ConfigError);
    expect(() => loadConfig({ MCP_MAX_SESSIONS: "0" })).toThrow(/MCP_MAX_SESSIONS/);
  });

  it("rejects non-integer MCP_SESSION_IDLE_MS", () => {
    expect(() => loadConfig({ MCP_SESSION_IDLE_MS: "3.14" })).toThrowError(ConfigError);
  });

  it("rejects empty MCP_ALLOWED_HOSTS after filtering", () => {
    // "," and ", ,," both filter down to []. Must fail loudly to avoid
    // a silent broken-but-running deploy.
    expect(() => loadConfig({ MCP_ALLOWED_HOSTS: "," })).toThrowError(ConfigError);
    expect(() => loadConfig({ MCP_ALLOWED_HOSTS: ",  ,  ," })).toThrowError(ConfigError);
  });

  it("accepts a single allowed host with whitespace padding", () => {
    const cfg = loadConfig({ MCP_ALLOWED_HOSTS: "  mcp.e2a.dev  " });
    expect(cfg.allowedHosts).toEqual(["mcp.e2a.dev"]);
  });

  it("allows port 0 (OS-assigned)", () => {
    const cfg = loadConfig({ PORT: "0" });
    expect(cfg.port).toBe(0);
  });

  it("defaults E2A_TRUST_PROXY to loopback", () => {
    expect(loadConfig({}).trustProxy).toBe("loopback");
  });

  it("parses E2A_TRUST_PROXY booleans", () => {
    expect(loadConfig({ E2A_TRUST_PROXY: "true" }).trustProxy).toBe(true);
    expect(loadConfig({ E2A_TRUST_PROXY: "false" }).trustProxy).toBe(false);
  });

  it("parses a bare integer E2A_TRUST_PROXY as a hop count", () => {
    // Express reads a numeric *string* as a subnet, so it must become a
    // real number to mean "trust N hops".
    expect(loadConfig({ E2A_TRUST_PROXY: "1" }).trustProxy).toBe(1);
  });

  it("passes through a preset / subnet E2A_TRUST_PROXY verbatim", () => {
    expect(loadConfig({ E2A_TRUST_PROXY: "uniquelocal" }).trustProxy).toBe("uniquelocal");
    expect(loadConfig({ E2A_TRUST_PROXY: "10.0.0.0/8" }).trustProxy).toBe("10.0.0.0/8");
  });
});
