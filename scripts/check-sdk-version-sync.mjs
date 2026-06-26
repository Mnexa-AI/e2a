#!/usr/bin/env node
// Guardrail: every in-repo consumer of @e2a/sdk must declare a range that the
// CURRENT workspace SDK version satisfies. Otherwise npm resolves the consumer
// against the stale PUBLISHED SDK from the registry instead of linking the
// in-repo workspace — which is exactly how the MCP shipped on @e2a/sdk 3.x
// after the SDK went to 4.0.0 and started returning `dnsRecords: {}` (the wire
// `dns_records` array deserialized into the old object model).
//
// When the SDK majors, this FAILS CI for every consumer still on the old major,
// forcing a deliberate bump + a check of the consumer's code for breaking
// changes. Dependency-free (no semver package needed).

import { readFileSync } from "node:fs";

const CONSUMERS = ["mcp", "cli"];
const SDK = "@e2a/sdk";

const read = (p) => JSON.parse(readFileSync(new URL(`../${p}`, import.meta.url), "utf8"));
const majorOf = (v) => {
  const m = String(v).match(/(\d+)\./);
  return m ? m[1] : null;
};

const sdkPkg = read("sdks/typescript/package.json");
const sdkVersion = sdkPkg.version;
const sdkMajor = majorOf(sdkVersion);

let failed = false;
for (const c of CONSUMERS) {
  const pkg = read(`${c}/package.json`);
  const range = pkg.dependencies?.[SDK] ?? pkg.devDependencies?.[SDK];
  if (!range) continue; // consumer doesn't use the SDK
  // "*" / "workspace:*" always track the workspace — never skew.
  if (range === "*" || range.startsWith("workspace:")) {
    console.log(`✓ ${c}: ${SDK} "${range}" always tracks the workspace`);
    continue;
  }
  const rangeMajor = majorOf(range);
  if (rangeMajor === sdkMajor) {
    console.log(`✓ ${c}: ${SDK} "${range}" matches workspace SDK ${sdkVersion}`);
    continue;
  }
  console.error(
    `✗ ${c}: ${SDK} "${range}" does NOT match the workspace SDK ${sdkVersion} (major ${sdkMajor}).\n` +
      `    Fix: bump ${c}/package.json ${SDK} to "^${sdkMajor}.0.0" (or "*"), run npm install,\n` +
      `    and update ${c} code for any breaking SDK changes.`,
  );
  failed = true;
}

if (failed) {
  console.error(
    `\nInternal SDK version skew detected. An in-repo consumer's @e2a/sdk range is not satisfied\n` +
      `by the workspace SDK ${sdkVersion}, so npm links a STALE published SDK instead of the\n` +
      `workspace. This is the failure that returned dnsRecords: {} over the MCP after the SDK\n` +
      `majored. Bump the consumer(s) above and re-run.`,
  );
  process.exit(1);
}
console.log(`\nAll internal @e2a/sdk consumers are in sync with workspace SDK ${sdkVersion}.`);
