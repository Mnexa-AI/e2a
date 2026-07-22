import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const repoRoot = fileURLToPath(new URL("../", import.meta.url));

function validateCatalog(label, value) {
  if (!Array.isArray(value) || value.some((name) => typeof name !== "string")) {
    throw new Error(`${label} MCP tool catalog must contain only strings`);
  }
  const sortedUnique = [...new Set(value)].sort();
  if (JSON.stringify(value) !== JSON.stringify(sortedUnique)) {
    throw new Error(`${label} MCP tool catalog must be sorted and unique`);
  }
  return value;
}

export function assertAdditiveToolCatalog(base, revision) {
  const baseNames = validateCatalog("base", base);
  const revisionNames = validateCatalog("revision", revision);
  const revisionSet = new Set(revisionNames);
  const removed = baseNames.filter((name) => !revisionSet.has(name));
  if (removed.length > 0) {
    throw new Error(`removed MCP tool names: ${removed.join(", ")}`);
  }
}

function readCatalog(source) {
  let raw;
  if (existsSync(source)) {
    raw = readFileSync(source, "utf8");
  } else {
    const shown = spawnSync("git", ["show", source], {
      cwd: repoRoot,
      encoding: "utf8",
    });
    if (shown.status !== 0) {
      const detail = shown.stderr.trim() || `git show exited ${shown.status}`;
      throw new Error(`MCP tool catalog is not a file or Git object: ${source} (${detail})`);
    }
    raw = shown.stdout;
  }
  try {
    return JSON.parse(raw);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`invalid MCP tool catalog JSON at ${source}: ${message}`);
  }
}

function main(args) {
  if (args.length < 1 || args.length > 2) {
    console.error("usage: node scripts/check-mcp-tool-compat.mjs <base-catalog> [revision-catalog]");
    process.exitCode = 2;
    return;
  }
  const [baseSource, revisionSource = resolve(repoRoot, "mcp/tool-names.v1.json")] = args;
  try {
    const base = readCatalog(baseSource);
    const revision = readCatalog(revisionSource);
    assertAdditiveToolCatalog(base, revision);
    console.log(`MCP tool catalog is additive (${base.length} base, ${revision.length} revision)`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  main(process.argv.slice(2));
}
