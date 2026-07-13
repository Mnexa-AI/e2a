import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "./client.js";
import { registerMessageTools } from "./tools/messages.js";
import { registerAgentTools } from "./tools/agents.js";
import { registerDomainTools } from "./tools/domains.js";
import { registerReviewTools } from "./tools/review.js";
import { registerWebhookTools } from "./tools/webhooks.js";
import { registerEventTools } from "./tools/events.js";
import { registerTemplateTools } from "./tools/templates.js";
import { registerApiKeyTools } from "./tools/apikeys.js";
import { toolNamesForScope } from "./tools/tiers.js";

export interface BuildServerOptions {
  client: McpClient;
  version?: string;
}

// gateRegistration intercepts server.registerTool so a session only registers
// the tools its credential scope is allowed to see (§6a). One seam gates every
// tool — the per-resource register*Tools functions stay scope-agnostic, and the
// tier classification lives solely in tiers.ts. Tools outside the allowed set
// are silently skipped (not registered → not listed → not callable). This is a
// surface/decision-space optimization; the backend still enforces scope, so a
// skipped tool is never the security boundary.
function gateRegistration(server: McpServer, allowed: ReadonlySet<string>): void {
  const original = server.registerTool.bind(server) as (...a: unknown[]) => unknown;
  server.registerTool = ((name: string, ...rest: unknown[]) => {
    if (!allowed.has(name)) {
      // Skip: not wired into the server's tool list. Returns undefined; the
      // register*Tools callers don't use the return value.
      return undefined as unknown as ReturnType<McpServer["registerTool"]>;
    }
    return original(name, ...rest) as ReturnType<McpServer["registerTool"]>;
  }) as McpServer["registerTool"];
}

export function buildServer({ client, version = "0.1.0" }: BuildServerOptions): McpServer {
  const server = new McpServer({
    name: "e2a",
    version,
  });
  // Scope-gate the surface to the connecting credential's tier (client.scope,
  // resolved at session init via whoami). account → full surface; agent →
  // runtime/inbox subset.
  gateRegistration(server, toolNamesForScope(client.scope));
  registerMessageTools(server, client);
  registerAgentTools(server, client);
  registerDomainTools(server, client);
  registerReviewTools(server, client);
  registerWebhookTools(server, client);
  registerEventTools(server, client);
  registerTemplateTools(server, client);
  registerApiKeyTools(server, client);
  return server;
}
