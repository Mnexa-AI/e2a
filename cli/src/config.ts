import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";
import { EXIT } from "./exit.js";

export interface Config {
  api_key: string;
  api_url: string;
  agent_email: string;
  /**
   * Shared mail domain the deployment uses for slug-based agent addresses
   * (e.g. "agents.example.com"), auto-discovered from `GET /v1/info` on
   * `e2a login` and cached here. Self-hosters with a different shared domain
   * can override via `E2A_SHARED_DOMAIN` or `e2a config set shared_domain ...`.
   * Defaults to the hosted product's shared domain so the public deployment
   * works zero-config.
   */
  shared_domain: string;
  /**
   * Scope of the stored api_key ("account" or "agent"), recorded by `e2a
   * login` (which now writes it on every path: browser, --agent exchange,
   * --with-key). Lets commands that need workspace-admin scope fail with a
   * precise message instead of a server 403. Absent for keys saved by older
   * CLIs or set out-of-band.
   */
  key_scope?: string;
}

const CONFIG_DIR = join(homedir(), ".e2a");
const CONFIG_PATH = join(CONFIG_DIR, "config.json");
const DEFAULT_URL = "https://e2a.dev";
const DEFAULT_SHARED_DOMAIN = "agents.e2a.dev";

export function loadConfig(): Config {
  const config: Config = {
    api_key: "",
    api_url: DEFAULT_URL,
    agent_email: "",
    shared_domain: DEFAULT_SHARED_DOMAIN,
  };

  // Read from file
  try {
    const raw = readFileSync(CONFIG_PATH, "utf-8");
    const file = JSON.parse(raw);
    if (file.api_key) config.api_key = file.api_key;
    if (file.api_url) config.api_url = file.api_url;
    if (file.agent_email) config.agent_email = file.agent_email;
    if (file.shared_domain) config.shared_domain = file.shared_domain;
    if (file.key_scope) config.key_scope = file.key_scope;
  } catch {
    // No config file yet
  }

  // Env vars override file. E2A_BASE_URL is accepted as an alias for E2A_URL
  // and E2A_AGENT_EMAIL selects the inbox — these are the exact names the
  // tether harness trains users to export, so ignoring them is a silent trap
  // for the CLI's primary scripting consumer. E2A_URL wins over the alias.
  if (process.env.E2A_API_KEY) config.api_key = process.env.E2A_API_KEY;
  if (process.env.E2A_URL) config.api_url = process.env.E2A_URL;
  else if (process.env.E2A_BASE_URL) config.api_url = process.env.E2A_BASE_URL;
  if (process.env.E2A_AGENT_EMAIL) config.agent_email = process.env.E2A_AGENT_EMAIL;
  if (process.env.E2A_SHARED_DOMAIN) config.shared_domain = process.env.E2A_SHARED_DOMAIN;

  return config;
}

export function saveConfig(updates: Partial<Config>): void {
  const current = loadConfig();
  const merged = { ...current, ...updates };

  // Read existing file to preserve fields we don't manage
  let existing: Record<string, string> = {};
  try {
    existing = JSON.parse(readFileSync(CONFIG_PATH, "utf-8"));
  } catch {
    // No existing file
  }

  // Don't write env-only values back to file, but preserve unrelated fields.
  const fileConfig: Record<string, string> = { ...existing };

  if ("api_key" in updates) {
    if (updates.api_key) fileConfig.api_key = updates.api_key;
    else delete fileConfig.api_key;
  } else if (!process.env.E2A_API_KEY && merged.api_key) {
    fileConfig.api_key = merged.api_key;
  }

  if ("api_url" in updates) {
    if (updates.api_url && updates.api_url !== DEFAULT_URL && !process.env.E2A_URL) {
      fileConfig.api_url = updates.api_url;
    } else {
      delete fileConfig.api_url;
    }
  } else if (!process.env.E2A_URL && merged.api_url !== DEFAULT_URL) {
    fileConfig.api_url = merged.api_url;
  } else if (!process.env.E2A_URL) {
    delete fileConfig.api_url;
  }

  if ("agent_email" in updates) {
    if (updates.agent_email) fileConfig.agent_email = updates.agent_email;
    else delete fileConfig.agent_email;
  } else if (!process.env.E2A_AGENT_EMAIL && merged.agent_email) {
    fileConfig.agent_email = merged.agent_email;
  } else if (!process.env.E2A_AGENT_EMAIL) {
    delete fileConfig.agent_email;
  }

  // key_scope has no env override and no default: persist when set, drop when
  // cleared. Login rewrites it whenever api_key changes, so it can't go stale
  // through the login path (a hand-edited api_key is on the user).
  if ("key_scope" in updates) {
    if (updates.key_scope) fileConfig.key_scope = updates.key_scope;
    else delete fileConfig.key_scope;
  }

  // Mirror the api_url policy: only persist non-default, non-env-overridden values.
  if ("shared_domain" in updates) {
    if (
      updates.shared_domain &&
      updates.shared_domain !== DEFAULT_SHARED_DOMAIN &&
      !process.env.E2A_SHARED_DOMAIN
    ) {
      fileConfig.shared_domain = updates.shared_domain;
    } else {
      delete fileConfig.shared_domain;
    }
  } else if (!process.env.E2A_SHARED_DOMAIN && merged.shared_domain !== DEFAULT_SHARED_DOMAIN) {
    fileConfig.shared_domain = merged.shared_domain;
  } else if (!process.env.E2A_SHARED_DOMAIN) {
    delete fileConfig.shared_domain;
  }

  mkdirSync(CONFIG_DIR, { recursive: true });
  writeFileSync(CONFIG_PATH, JSON.stringify(fileConfig, null, 2) + "\n", {
    mode: 0o600,
  });
}

export function requireApiKey(config: Config): string {
  if (!config.api_key) {
    // Missing credentials are an auth failure per the documented exit-code
    // contract (4) — never 1, which scripts treat as retryable-transient.
    // Name BOTH acquisition paths: `login` needs a browser on this machine;
    // headless boxes use --with-key or the env var.
    process.stderr.write(
      "Not authenticated. Run: e2a login (browser) or e2a login --with-key (headless), or set E2A_API_KEY\n",
    );
    process.exit(EXIT.AUTH);
  }
  return config.api_key;
}
