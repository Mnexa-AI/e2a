import { describe, expect, it, vi } from "vitest";
import { ConfigError, loadConfig, logJson } from "../src/bin/http.js";

describe("bin/http loadConfig", () => {
  it("returns defaults when env is empty", () => {
    const cfg = loadConfig({});
    expect(cfg).toEqual({
      port: 3000,
      // This server is a pure API client, so it defaults to the API host —
      // not the deployment root the CLI's E2A_URL points at.
      baseUrl: "https://api.e2a.dev",
      allowedHosts: ["api.e2a.dev"],
      sessionIdleMs: 5 * 60_000,
      maxSessions: 500,
    });
  });

  it("parses valid values (canonical E2A_API_URL)", () => {
    const cfg = loadConfig({
      PORT: "8080",
      E2A_API_URL: "https://api.staging.e2a.dev",
      MCP_ALLOWED_HOSTS: "api.e2a.dev,mcp-staging.e2a.dev",
      MCP_SESSION_IDLE_MS: "60000",
      MCP_MAX_SESSIONS: "100",
    });
    expect(cfg).toEqual({
      port: 8080,
      baseUrl: "https://api.staging.e2a.dev",
      allowedHosts: ["api.e2a.dev", "mcp-staging.e2a.dev"],
      sessionIdleMs: 60_000,
      maxSessions: 100,
    });
  });

  it.each([["E2A_URL"], ["E2A_BASE_URL"]])(
    "falls back to %s when E2A_API_URL is unset (structured deprecation log)",
    (legacy) => {
      const lines: string[] = [];
      const warn = vi.spyOn(process.stderr, "write").mockImplementation((chunk) => {
        lines.push(String(chunk));
        return true;
      });
      const cfg = loadConfig({ [legacy]: "https://legacy.example.com" });
      expect(cfg.baseUrl).toBe("https://legacy.example.com");
      // The deprecation notice is emitted as one structured JSON line that
      // Cloud Logging can parse — severity + event + a human-readable message.
      const entry = JSON.parse(lines.at(-1)!);
      expect(entry).toMatchObject({ severity: "WARNING", event: "e2a_api_url_legacy_name" });
      expect(entry.message).toContain(legacy);
      warn.mockRestore();
    },
  );

  it("prefers canonical E2A_API_URL over both legacy names", () => {
    const cfg = loadConfig({
      E2A_API_URL: "https://canonical.example.com",
      E2A_URL: "https://deployment-root.example.com",
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
    const cfg = loadConfig({ MCP_ALLOWED_HOSTS: "  api.e2a.dev  " });
    expect(cfg.allowedHosts).toEqual(["api.e2a.dev"]);
  });

  it("allows port 0 (OS-assigned)", () => {
    const cfg = loadConfig({ PORT: "0" });
    expect(cfg.port).toBe(0);
  });
});

describe("logJson", () => {
  it("emits a single-line JSON object with severity, event, message and fields", () => {
    const lines: string[] = [];
    const spy = vi.spyOn(process.stderr, "write").mockImplementation((chunk) => {
      lines.push(String(chunk));
      return true;
    });
    logJson("INFO", "listening", "e2a-mcp-http listening on :3000", {
      port: 3000,
      allowedHosts: ["api.e2a.dev"],
    });
    spy.mockRestore();

    expect(lines).toHaveLength(1);
    // Exactly one line: trailing newline, no embedded newlines (so Cloud
    // Logging treats it as a single structured entry).
    expect(lines[0].endsWith("\n")).toBe(true);
    expect(lines[0].trimEnd()).not.toContain("\n");
    expect(JSON.parse(lines[0])).toEqual({
      severity: "INFO",
      event: "listening",
      message: "e2a-mcp-http listening on :3000",
      port: 3000,
      allowedHosts: ["api.e2a.dev"],
    });
  });
});
