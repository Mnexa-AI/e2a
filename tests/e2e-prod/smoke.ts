import { ApiClient } from "./harness/client.ts";

const client = new ApiClient();
console.log(`API:  ${client.env.apiUrl}`);
console.log(`Key:  ${client.env.apiKey.slice(0, 12)}…`);
console.log(`Agent: ${client.env.primaryAgentEmail}`);
console.log(`Shared domain: ${client.env.sharedDomain}`);
console.log(`Rate limit: ${client.env.rateLimitRps} RPS`);
console.log("");

const info = await client.get("/v1/info", { expect: 200 });
console.log(`GET /v1/info  ${info.status}  ${info.latencyMs.toFixed(0)}ms`);
console.log(`  ${JSON.stringify(info.body)}`);

const agent = await client.get(`/v1/agents/${encodeURIComponent(client.env.primaryAgentEmail)}`, { expect: 200 });
console.log(`GET /agents/${client.env.primaryAgentEmail}  ${agent.status}  ${agent.latencyMs.toFixed(0)}ms`);
console.log(`  ${JSON.stringify(agent.body)}`);

const agents = await client.get<{ agents?: unknown[] }>("/v1/agents", { expect: 200 });
console.log(`GET /agents  ${agents.status}  ${agents.latencyMs.toFixed(0)}ms  count=${agents.body?.agents?.length ?? "?"}`);

const domains = await client.get<{ domains?: unknown[] }>("/v1/domains", { expect: 200 });
console.log(`GET /domains  ${domains.status}  ${domains.latencyMs.toFixed(0)}ms  count=${domains.body?.domains?.length ?? "?"}`);

const msgsPath = `/v1/agents/${encodeURIComponent(client.env.primaryAgentEmail)}/messages`;
const msgs = await client.get<{ items?: unknown[] }>(msgsPath, { query: { limit: 5 }, expect: 200 });
console.log(`GET /agents/${client.env.primaryAgentEmail}/messages?limit=5  ${msgs.status}  ${msgs.latencyMs.toFixed(0)}ms  count=${msgs.body?.items?.length ?? "?"}`);

console.log("\nSmoke OK");
