import { loadConfig, saveConfig, Config } from "../config.js";
import { EXIT } from "../exit.js";

const VALID_KEYS: (keyof Config)[] = ["api_key", "api_url", "agent_email", "shared_domain", "key_scope"];

export async function config(args: string[]): Promise<void> {
  const subcommand = args[0];

  if (!subcommand || subcommand === "list") {
    showAll();
    return;
  }

  if (subcommand === "get") {
    const key = args[1];
    if (!key) {
      process.stderr.write("Usage: e2a config get <key>\n");
      process.exit(EXIT.USAGE);
    }
    if (!VALID_KEYS.includes(key as keyof Config)) {
      process.stderr.write(`Unknown key: ${key}\nValid keys: ${VALID_KEYS.join(", ")}\n`);
      process.exit(EXIT.USAGE);
    }
    const cfg = loadConfig();
    const value = cfg[key as keyof Config];
    if (value) {
      process.stdout.write(`${value}\n`);
    }
    return;
  }

  if (subcommand === "set") {
    const key = args[1];
    const value = args[2];
    if (!key || value === undefined) {
      process.stderr.write("Usage: e2a config set <key> <value>\n");
      process.exit(EXIT.USAGE);
    }
    if (!VALID_KEYS.includes(key as keyof Config)) {
      process.stderr.write(`Unknown key: ${key}\nValid keys: ${VALID_KEYS.join(", ")}\n`);
      process.exit(EXIT.USAGE);
    }
    // key_scope is an enum, not free text — a persisted typo would feed any
    // future scope preflight confidently wrong data.
    if (key === "key_scope" && value !== "account" && value !== "agent") {
      process.stderr.write(`key_scope must be "account" or "agent"\n`);
      process.exit(EXIT.USAGE);
    }
    saveConfig({ [key]: value });
    process.stdout.write(`${key}=${value}\n`);
    return;
  }

  // Treat bare key as shorthand for "get"
  if (VALID_KEYS.includes(subcommand as keyof Config)) {
    const cfg = loadConfig();
    const value = cfg[subcommand as keyof Config];
    if (value) {
      process.stdout.write(`${value}\n`);
    }
    return;
  }

  process.stderr.write(
    "Usage: e2a config [list|get <key>|set <key> <value>]\n" +
      `Valid keys: ${VALID_KEYS.join(", ")}\n`,
  );
  process.exit(EXIT.USAGE);
}

function showAll(): void {
  const cfg = loadConfig();
  for (const key of VALID_KEYS) {
    const value = cfg[key];
    if (key === "api_key" && value) {
      process.stdout.write(`${key}=${value.slice(0, 8)}…\n`);
    } else {
      process.stdout.write(`${key}=${value || ""}\n`);
    }
  }
}
