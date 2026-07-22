import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import type { McpClient } from "../src/client.js";

// Drift guard (mirrors the repo's spec-check / generate-sdk-check pattern):
// the hosted MCP server follows the product release, not the retired npm
// package. The registry manifest matches root VERSION, while deployed images
// may inject an immutable build identity through MCP_SERVER_VERSION.
const mcpDir = fileURLToPath(new URL("..", import.meta.url));
const productVersion = readFileSync(new URL("../../VERSION", import.meta.url), "utf8").trim();

function readJson(relPath: string): Record<string, unknown> {
  return JSON.parse(readFileSync(`${mcpDir}${relPath}`, "utf8"));
}

function readText(relPath: string): string {
  return readFileSync(`${mcpDir}${relPath}`, "utf8");
}

// Minimal stub — buildServer only needs `client.scope` (for §6a tool
// gating) at construction time; no tool is actually invoked here.
function makeStubClient(): McpClient {
  return { scope: "account" } as unknown as McpClient;
}

async function reportedVersion(): Promise<{ name: string; version: string } | undefined> {
  vi.resetModules();
  const { buildServer } = await import("../src/server.js");
  const server = buildServer({ client: makeStubClient() });
  const client = new Client({ name: "version-test-client", version: "0.0.0" });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([server.connect(serverTransport), client.connect(clientTransport)]);
  const version = client.getServerVersion();
  await Promise.all([client.close(), server.close()]);
  return version;
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("MCP server version — product and build identity", () => {
  it("server.json's version matches root VERSION", () => {
    const manifest = readJson("server.json");
    expect(manifest.version).toBe(productVersion);
  });

  it("the live handshake defaults to root VERSION", async () => {
    vi.stubEnv("MCP_SERVER_VERSION", "");
    expect(await reportedVersion()).toEqual({ name: "e2a", version: productVersion });
  });

  it("the live handshake prefers the injected deployment version", async () => {
    vi.stubEnv("MCP_SERVER_VERSION", "1.0.0+sha.abcdef123456");
    expect(await reportedVersion()).toEqual({
      name: "e2a",
      version: "1.0.0+sha.abcdef123456",
    });
  });

  it("persists the injected version in the runtime image", () => {
    const dockerfile = readText("Dockerfile");
    expect(dockerfile).toContain("ARG MCP_SERVER_VERSION");
    expect(dockerfile).toContain("ENV MCP_SERVER_VERSION=${MCP_SERVER_VERSION}");
    expect(dockerfile).toContain("COPY VERSION ./VERSION");
  });

  it("passes the computed build identity into the image build", () => {
    const workflow = readFileSync(
      new URL("../../.github/workflows/publish-mcp-http.yml", import.meta.url),
      "utf8",
    );
    expect(workflow).toContain("id: server-version");
    expect(workflow).toContain("MCP_SERVER_VERSION=${{ steps.server-version.outputs.value }}");
  });
});
