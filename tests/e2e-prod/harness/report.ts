import { writeFileSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";

export interface Finding {
  severity: "info" | "warn" | "fail";
  suite: string;
  test: string;
  message: string;
  detail?: unknown;
}

const findings: Finding[] = [];

export function record(f: Finding): void {
  findings.push(f);
  const tag = f.severity === "fail" ? "FAIL" : f.severity === "warn" ? "WARN" : "INFO";
  console.log(`[${tag}] ${f.suite} / ${f.test}: ${f.message}`);
}

export function info(suite: string, test: string, message: string, detail?: unknown) {
  record({ severity: "info", suite, test, message, detail });
}
export function warn(suite: string, test: string, message: string, detail?: unknown) {
  record({ severity: "warn", suite, test, message, detail });
}
export function fail(suite: string, test: string, message: string, detail?: unknown) {
  record({ severity: "fail", suite, test, message, detail });
}

export function writeReport(path: string): void {
  mkdirSync(dirname(path), { recursive: true });
  const summary = {
    timestamp: new Date().toISOString(),
    counts: {
      info: findings.filter((f) => f.severity === "info").length,
      warn: findings.filter((f) => f.severity === "warn").length,
      fail: findings.filter((f) => f.severity === "fail").length,
    },
    findings,
  };
  writeFileSync(path, JSON.stringify(summary, null, 2));
}

export function getFindings(): readonly Finding[] {
  return findings;
}
