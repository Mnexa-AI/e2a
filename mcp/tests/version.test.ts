import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import type { McpClient } from "../src/client.js";
import { buildServer } from "../src/server.js";

// Drift guard (mirrors the repo's spec-check / generate-sdk-check pattern):
// mcp/package.json is the single source of truth for the reported MCP
// server version. mcp/server.json (the registry manifest) and the live
// initialize handshake's serverInfo.version must never silently diverge
// from it. Before this test, all three disagreed (0.5.0 / 0.4.0 / 0.1.0)
// with no shared constant — this is the in-tree gate that would have
// caught it and prevents a future re-drift.
const mcpDir = fileURLToPath(new URL("..", import.meta.url));

function readJson(relPath: string): Record<string, unknown> {
  return JSON.parse(readFileSync(`${mcpDir}${relPath}`, "utf8"));
}

// Minimal stub — buildServer only needs `client.scope` (for §6a tool
// gating) at construction time; no tool is actually invoked here.
function makeStubClient(): McpClient {
  return { scope: "account" } as unknown as McpClient;
}

describe("MCP server version — single source of truth (package.json)", () => {
  it("server.json's version matches package.json's version", () => {
    const pkg = readJson("package.json");
    const manifest = readJson("server.json");
    expect(manifest.version).toBe(pkg.version);
  });

  it("the live initialize handshake reports package.json's version", async () => {
    const pkg = readJson("package.json");
    const server = buildServer({ client: makeStubClient() });
    const client = new Client({ name: "version-test-client", version: "0.0.0" });
    const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
    await Promise.all([server.connect(serverTransport), client.connect(clientTransport)]);

    expect(client.getServerVersion()).toEqual({ name: "e2a", version: pkg.version });
  });
});
