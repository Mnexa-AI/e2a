import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

interface Finding {
  severity: "info" | "warn" | "fail";
  suite: string;
  test: string;
  message: string;
}
interface Report {
  timestamp: string;
  counts: { info: number; warn: number; fail: number };
  findings: Finding[];
}

const dir = "./reports";
const files = readdirSync(dir).filter((f) => f.endsWith(".json") && f !== "consolidated.json");
const all: Finding[] = [];
for (const f of files) {
  const r = JSON.parse(readFileSync(join(dir, f), "utf-8")) as Report;
  all.push(...r.findings);
}

const bySeverity = {
  fail: all.filter((f) => f.severity === "fail"),
  warn: all.filter((f) => f.severity === "warn"),
  info: all.filter((f) => f.severity === "info"),
};

const out = {
  timestamp: new Date().toISOString(),
  total_findings: all.length,
  counts: { fail: bySeverity.fail.length, warn: bySeverity.warn.length, info: bySeverity.info.length },
  fail: bySeverity.fail,
  warn: bySeverity.warn,
  info: bySeverity.info,
};
writeFileSync(join(dir, "consolidated.json"), JSON.stringify(out, null, 2));

console.log(`Consolidated ${all.length} findings from ${files.length} suite reports:`);
console.log(`  FAIL: ${bySeverity.fail.length}`);
console.log(`  WARN: ${bySeverity.warn.length}`);
console.log(`  INFO: ${bySeverity.info.length}`);
console.log("");
if (bySeverity.fail.length > 0) {
  console.log("=== FAIL ===");
  for (const f of bySeverity.fail) console.log(`  [${f.suite}] ${f.test}: ${f.message}`);
  console.log("");
}
if (bySeverity.warn.length > 0) {
  console.log("=== WARN ===");
  for (const f of bySeverity.warn) console.log(`  [${f.suite}] ${f.test}: ${f.message}`);
  console.log("");
}
console.log("=== INFO (highlights) ===");
for (const f of bySeverity.info) console.log(`  [${f.suite}] ${f.test}: ${f.message}`);
