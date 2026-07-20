import { afterEach, describe, expect, it, vi } from "vitest";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const mockedHome = vi.hoisted(() => ({ path: "" }));

vi.mock("node:os", async (importOriginal) => {
  const actual = await importOriginal<typeof import("node:os")>();
  return { ...actual, homedir: () => mockedHome.path };
});

describe("config file permissions", () => {
  let testHome = "";

  afterEach(() => {
    if (testHome) rmSync(testHome, { recursive: true, force: true });
    testHome = "";
  });

  it("tightens a permissive existing config before returning", async () => {
    testHome = mkdtempSync(join(tmpdir(), "e2a-cli-config-"));
    mockedHome.path = testHome;
    const configDir = join(testHome, ".e2a");
    const configPath = join(configDir, "config.json");
    mkdirSync(configDir);
    writeFileSync(configPath, "{}\n", { mode: 0o644 });
    expect(statSync(configPath).mode & 0o777).toBe(0o644);

    vi.resetModules();
    const { saveConfig } = await import("../config.js");
    saveConfig({ api_key: "e2a_secret" });

    expect(statSync(configPath).mode & 0o777).toBe(0o600);
    expect(JSON.parse(readFileSync(configPath, "utf8")).api_key).toBe("e2a_secret");
  });
});
