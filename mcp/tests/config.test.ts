import { describe, expect, it } from "vitest";
import { ConfigError, loadConfig } from "../src/config.js";

describe("loadConfig", () => {
  it("reads required and optional vars", () => {
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_BASE_URL: "https://api.example.com",
      E2A_AGENT_EMAIL: "bot@example.com",
    });
    expect(cfg).toEqual({
      apiKey: "key_123",
      baseUrl: "https://api.example.com",
      agentEmail: "bot@example.com",
    });
  });

  it("treats empty optional vars as undefined", () => {
    const cfg = loadConfig({
      E2A_API_KEY: "key_123",
      E2A_BASE_URL: "",
      E2A_AGENT_EMAIL: "",
    });
    expect(cfg.baseUrl).toBeUndefined();
    expect(cfg.agentEmail).toBeUndefined();
  });

  it("throws ConfigError when API key is missing", () => {
    expect(() => loadConfig({})).toThrowError(ConfigError);
    expect(() => loadConfig({})).toThrow(/E2A_API_KEY/);
  });
});
