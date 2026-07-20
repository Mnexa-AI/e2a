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

    // Warn if this key is interfered with by an environment variable.
    // Two distinct cases: (a) NOT PERSISTED AT ALL, or (b) PERSISTED BUT SHADOWED ON READ.
    warnIfEnvVarInterferes(key);
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

/**
 * Warn if an environment variable interferes with a config key.
 * There are two cases:
 * (a) NOT PERSISTED AT ALL: api_url/E2A_URL, shared_domain/E2A_SHARED_DOMAIN
 * (b) PERSISTED BUT SHADOWED ON READ: api_key/E2A_API_KEY, agent_email/E2A_AGENT_EMAIL
 */
function warnIfEnvVarInterferes(key: string): void {
  // Case (a): NOT PERSISTED — the write was discarded entirely
  if (key === "api_url" && process.env.E2A_URL) {
    process.stderr.write(
      "e2a: api_url was not saved — E2A_URL environment variable is set and blocks writes.\n" +
        "     Unset E2A_URL to persist this configuration.\n",
    );
    return;
  }

  if (key === "shared_domain" && process.env.E2A_SHARED_DOMAIN) {
    process.stderr.write(
      "e2a: shared_domain was not saved — E2A_SHARED_DOMAIN environment variable is set and blocks writes.\n" +
        "     Unset E2A_SHARED_DOMAIN to persist this configuration.\n",
    );
    return;
  }

  // Case (b): PERSISTED BUT SHADOWED ON READ — the file was written but env var takes precedence
  if (key === "api_key" && process.env.E2A_API_KEY) {
    process.stderr.write(
      "e2a: api_key was saved, but E2A_API_KEY environment variable will take precedence.\n" +
        "     `e2a config get api_key` will show the env var value, not your saved config.\n" +
        "     Unset E2A_API_KEY to use the saved value.\n",
    );
    return;
  }

  if (key === "agent_email" && process.env.E2A_AGENT_EMAIL) {
    process.stderr.write(
      "e2a: agent_email was saved, but E2A_AGENT_EMAIL environment variable will take precedence.\n" +
        "     `e2a config get agent_email` will show the env var value, not your saved config.\n" +
        "     Unset E2A_AGENT_EMAIL to use the saved value.\n",
    );
    return;
  }
}
