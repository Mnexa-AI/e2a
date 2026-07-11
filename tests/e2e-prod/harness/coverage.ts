import { writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

// API-coverage recorder. Every request the ApiClient issues is recorded here as a
// "METHOD /concrete/path" pair. `node --test` runs each suite FILE in its own
// process, so each process flushes its own coverage shard (coverage-<pid>.json)
// to reports/ on exit; the Python coverage gate (coverage_gate.py) unions the
// shards, maps each concrete path to an OpenAPI operationId, and fails if any spec
// operation went unexercised. Recording is best-effort and never throws.
const REPORTS_DIR = fileURLToPath(new URL("../reports/", import.meta.url));

const covered = new Set<string>();
let installed = false;

export function recordRequest(method: string, pathname: string): void {
  covered.add(`${method.toUpperCase()} ${pathname}`);
  if (!installed) {
    installed = true;
    // Sync flush on process exit — the only hook guaranteed to run once the
    // suite's tests are done (async handlers can't run during 'exit').
    process.on("exit", () => {
      if (covered.size === 0) return;
      try {
        mkdirSync(REPORTS_DIR, { recursive: true });
        writeFileSync(`${REPORTS_DIR}coverage-${process.pid}.json`, JSON.stringify([...covered]));
      } catch {
        /* best-effort: coverage is advisory, must never fail a suite */
      }
    });
  }
}
