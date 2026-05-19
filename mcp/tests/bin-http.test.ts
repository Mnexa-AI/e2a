import { describe, expect, it } from "vitest";
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
    });
  });

  it("parses valid values", () => {
    const cfg = loadConfig({
      PORT: "8080",
      E2A_BASE_URL: "https://staging.e2a.dev",
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
    });
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
});
