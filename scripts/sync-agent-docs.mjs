#!/usr/bin/env node

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const AGENT_DOC_MIRRORS = [
  ["plugins/e2a/docs/e2a.md", "web/public/e2a.md"],
  ["plugins/e2a/docs/templates.md", "web/public/templates.md"],
];

const usage = "usage: node scripts/sync-agent-docs.mjs [--check]";

export function parseArgs(args) {
  if (args.length === 0) return { check: false };
  if (args.length === 1 && args[0] === "--check") return { check: true };
  if (args.length === 1) throw new Error(`unknown option: ${args[0]}\n${usage}`);
  throw new Error(usage);
}

export async function syncAgentDocs({ repoRoot, check, log = console.log }) {
  const mismatches = [];

  for (const [source, target] of AGENT_DOC_MIRRORS) {
    let canonical;
    try {
      canonical = await readFile(join(repoRoot, source));
    } catch (error) {
      if (error?.code === "ENOENT") {
        throw new Error(`missing canonical agent doc: ${source}`);
      }
      throw error;
    }

    let hosted;
    try {
      hosted = await readFile(join(repoRoot, target));
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }

    if (hosted?.equals(canonical)) continue;

    if (check) {
      mismatches.push(
        `${hosted === undefined ? "missing" : "stale"} hosted agent doc: ${target}`,
      );
      continue;
    }

    const targetPath = join(repoRoot, target);
    await mkdir(dirname(targetPath), { recursive: true });
    await writeFile(targetPath, canonical);
    log(`synced ${source} -> ${target}`);
  }

  if (mismatches.length > 0) {
    throw new Error(mismatches.join("\n"));
  }
}

const scriptPath = fileURLToPath(import.meta.url);
const isMain = process.argv[1] && resolve(process.argv[1]) === scriptPath;

if (isMain) {
  try {
    const options = parseArgs(process.argv.slice(2));
    const repoRoot = resolve(dirname(scriptPath), "..");
    await syncAgentDocs({ repoRoot, ...options });
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
