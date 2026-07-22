import { readFileSync } from "node:fs";

const productVersionUrl = new URL("../../VERSION", import.meta.url);

/** Resolve the identity reported in the MCP initialize handshake. */
export function resolveServerVersion(env: NodeJS.ProcessEnv = process.env): string {
  const deployedVersion = env.MCP_SERVER_VERSION?.trim();
  if (deployedVersion) return deployedVersion;

  const productVersion = readFileSync(productVersionUrl, "utf8").trim();
  if (!productVersion) {
    throw new Error("root VERSION is empty; cannot determine MCP server version");
  }
  return productVersion;
}
