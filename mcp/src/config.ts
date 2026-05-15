export interface McpConfig {
  apiKey: string;
  baseUrl?: string;
  agentEmail?: string;
}

export class ConfigError extends Error {}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): McpConfig {
  const apiKey = env.E2A_API_KEY;
  if (!apiKey) {
    throw new ConfigError(
      "E2A_API_KEY is required. Set it in the MCP server's environment (typically via your ADK MCPToolset `env` config).",
    );
  }
  return {
    apiKey,
    baseUrl: env.E2A_BASE_URL || undefined,
    agentEmail: env.E2A_AGENT_EMAIL || undefined,
  };
}
