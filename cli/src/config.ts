import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

export interface Config {
  api_key: string;
  api_url: string;
  agent_email: string;
  /**
   * Shared mail domain the deployment uses for slug-based agent registration
   * (e.g. "agents.example.com"). The CLI uses this to expand bare slugs into
   * full email addresses when calling agent-scoped commands like
   * `e2a agents update my-bot`. Self-hosters with a different shared domain
   * should override via `E2A_SHARED_DOMAIN` or `e2a config set
   * shared_domain ...`. Defaults to the hosted product's shared domain so
   * the public deployment works zero-config.
   */
  shared_domain: string;
}

const CONFIG_DIR = join(homedir(), ".e2a");
const CONFIG_PATH = join(CONFIG_DIR, "config.json");
const DEFAULT_URL = "https://api.e2a.dev";
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
  } catch {
    // No config file yet
  }

  // Env vars override file
  if (process.env.E2A_API_KEY) config.api_key = process.env.E2A_API_KEY;
  if (process.env.E2A_URL) config.api_url = process.env.E2A_URL;
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
  } else if (merged.agent_email) {
    fileConfig.agent_email = merged.agent_email;
  } else {
    delete fileConfig.agent_email;
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
    process.stderr.write("Not authenticated. Run: e2a login\n");
    process.exit(1);
  }
  return config.api_key;
}
