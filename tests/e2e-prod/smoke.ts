import { ApiClient } from "./harness/client.ts";

const client = new ApiClient();
console.log(`API:  ${client.env.apiUrl}`);
console.log(`Key:  ${client.env.apiKey.slice(0, 12)}…`);
console.log(`Agent: ${client.env.primaryAgentEmail}`);
console.log(`Shared domain: ${client.env.sharedDomain}`);
console.log(`Rate limit: ${client.env.rateLimitRps} RPS`);
console.log("");

const info = await client.get("/api/v1/info", { expect: 200 });
console.log(`GET /api/v1/info  ${info.status}  ${info.latencyMs.toFixed(0)}ms`);
console.log(`  ${JSON.stringify(info.body)}`);

const agent = await client.get(`/api/v1/agents/${encodeURIComponent(client.env.primaryAgentEmail)}`, { expect: 200 });
console.log(`GET /agents/${client.env.primaryAgentEmail}  ${agent.status}  ${agent.latencyMs.toFixed(0)}ms`);
console.log(`  ${JSON.stringify(agent.body)}`);

const agents = await client.get<{ agents?: unknown[] }>("/api/v1/agents", { expect: 200 });
console.log(`GET /agents  ${agents.status}  ${agents.latencyMs.toFixed(0)}ms  count=${agents.body?.agents?.length ?? "?"}`);

const domains = await client.get<{ domains?: unknown[] }>("/api/v1/domains", { expect: 200 });
console.log(`GET /domains  ${domains.status}  ${domains.latencyMs.toFixed(0)}ms  count=${domains.body?.domains?.length ?? "?"}`);

const secrets = await client.get<{ secrets?: unknown[] }>("/api/v1/users/me/signing-secrets", { expect: 200 });
console.log(`GET /users/me/signing-secrets  ${secrets.status}  ${secrets.latencyMs.toFixed(0)}ms  count=${secrets.body?.secrets?.length ?? "?"}`);

const msgs = await client.get<{ messages?: unknown[] }>("/api/v1/messages", { query: { limit: 5 }, expect: 200 });
console.log(`GET /messages?limit=5  ${msgs.status}  ${msgs.latencyMs.toFixed(0)}ms  count=${msgs.body?.messages?.length ?? "?"}`);

console.log("\nSmoke OK");
