import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

// We need to test config logic without hitting the real filesystem.
// Import the module and mock fs operations.

vi.mock("node:fs", () => ({
  readFileSync: vi.fn(),
  writeFileSync: vi.fn(),
  mkdirSync: vi.fn(),
}));

const CONFIG_DIR = join(homedir(), ".e2a");
const CONFIG_PATH = join(CONFIG_DIR, "config.json");

// Import after mocks are set up
import { loadConfig, saveConfig, requireApiKey } from "../config.js";

describe("loadConfig", () => {
  beforeEach(() => {
    vi.mocked(readFileSync).mockReset();
    delete process.env.E2A_API_KEY;
    delete process.env.E2A_URL;
  });

  afterEach(() => {
    delete process.env.E2A_API_KEY;
    delete process.env.E2A_URL;
  });

  it("returns defaults when no config file exists", () => {
    vi.mocked(readFileSync).mockImplementation(() => {
      throw new Error("ENOENT");
    });

    const config = loadConfig();
    expect(config.api_key).toBe("");
    expect(config.api_url).toBe("https://e2a.dev");
    expect(config.agent_email).toBe("");
  });

  it("reads config from file", () => {
    vi.mocked(readFileSync).mockReturnValue(
      JSON.stringify({ api_key: "e2a_test", agent_email: "bot@agents.e2a.dev" }),
    );

    const config = loadConfig();
    expect(config.api_key).toBe("e2a_test");
    expect(config.agent_email).toBe("bot@agents.e2a.dev");
  });

  it("env vars override file values", () => {
    vi.mocked(readFileSync).mockReturnValue(
      JSON.stringify({ api_key: "e2a_fromfile", api_url: "https://old.dev" }),
    );
    process.env.E2A_API_KEY = "e2a_fromenv";
    process.env.E2A_URL = "https://custom.dev";

    const config = loadConfig();
    expect(config.api_key).toBe("e2a_fromenv");
    expect(config.api_url).toBe("https://custom.dev");
  });

  it("honors the tether env names: E2A_BASE_URL alias and E2A_AGENT_EMAIL", () => {
    vi.mocked(readFileSync).mockImplementation(() => {
      throw new Error("ENOENT");
    });
    process.env.E2A_BASE_URL = "https://api.selfhost.dev";
    process.env.E2A_AGENT_EMAIL = "tether@agents.e2a.dev";
    try {
      const config = loadConfig();
      expect(config.api_url).toBe("https://api.selfhost.dev");
      expect(config.agent_email).toBe("tether@agents.e2a.dev");

      // Canonical E2A_URL wins over the alias.
      process.env.E2A_URL = "https://canonical.dev";
      expect(loadConfig().api_url).toBe("https://canonical.dev");
    } finally {
      delete process.env.E2A_BASE_URL;
      delete process.env.E2A_AGENT_EMAIL;
      delete process.env.E2A_URL;
    }
  });
});

describe("saveConfig", () => {
  beforeEach(() => {
    vi.mocked(readFileSync).mockReset();
    vi.mocked(writeFileSync).mockReset();
    vi.mocked(mkdirSync).mockReset();
    delete process.env.E2A_API_KEY;
    delete process.env.E2A_URL;
  });

  afterEach(() => {
    delete process.env.E2A_API_KEY;
    delete process.env.E2A_URL;
  });

  it("creates config directory and writes file with 0o600 permissions", () => {
    vi.mocked(readFileSync).mockImplementation(() => {
      throw new Error("ENOENT");
    });

    saveConfig({ api_key: "e2a_newkey" });

    expect(mkdirSync).toHaveBeenCalledWith(CONFIG_DIR, { recursive: true });
    expect(writeFileSync).toHaveBeenCalledWith(
      CONFIG_PATH,
      expect.stringContaining("e2a_newkey"),
      { mode: 0o600 },
    );
  });

  it("preserves existing fields when updating", () => {
    // First read for loadConfig, second read for existing file
    vi.mocked(readFileSync).mockReturnValue(
      JSON.stringify({ api_key: "e2a_old", agent_email: "bot@agents.e2a.dev" }),
    );

    saveConfig({ api_key: "e2a_new" });

    const written = vi.mocked(writeFileSync).mock.calls[0][1] as string;
    const saved = JSON.parse(written);
    expect(saved.api_key).toBe("e2a_new");
    expect(saved.agent_email).toBe("bot@agents.e2a.dev");
  });

  it("removes agent_email when explicitly cleared", () => {
    vi.mocked(readFileSync).mockReturnValue(
      JSON.stringify({ api_key: "e2a_old", agent_email: "bot@agents.e2a.dev" }),
    );

    saveConfig({ agent_email: "" });

    const written = vi.mocked(writeFileSync).mock.calls[0][1] as string;
    const saved = JSON.parse(written);
    expect(saved.api_key).toBe("e2a_old");
    expect(saved.agent_email).toBeUndefined();
  });
});

describe("requireApiKey", () => {
  it("returns the key when present", () => {
    const key = requireApiKey({ api_key: "e2a_test", api_url: "", agent_email: "", shared_domain: "agents.e2a.dev" });
    expect(key).toBe("e2a_test");
  });

  it("exits when key is missing", () => {
    const mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    const mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);

    expect(() =>
      requireApiKey({ api_key: "", api_url: "", agent_email: "", shared_domain: "agents.e2a.dev" }),
    ).toThrow("process.exit");

    expect(mockStderr).toHaveBeenCalledWith(
      "Not authenticated. Run: e2a login (browser) or e2a login --with-key (headless), or set E2A_API_KEY\n",
    );
    // Missing credentials exit AUTH (4) per the scripting contract — not 1,
    // which scripts treat as a retryable transient error.
    expect(mockExit).toHaveBeenCalledWith(4);

    mockExit.mockRestore();
    mockStderr.mockRestore();
  });
});
