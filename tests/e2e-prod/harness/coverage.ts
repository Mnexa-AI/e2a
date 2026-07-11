import { writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

// API-coverage recorder. The ApiClient calls record(method, path, status) AFTER
// each response; an operation is marked covered only on a 2xx — a 401 auth-probe,
// a 404 unknown-id test, or a 422 validation-rejection issues a request at the
// route WITHOUT the operation's handler running to success, so counting it would
// over-claim coverage. Recording after the response also means a network failure
// (fetch throws) never counts.
//
// Shards live in reports/coverage/ (a SUBDIR) so they don't collide with the
// per-suite report JSONs in reports/ (consolidate.ts reads those as {findings}
// objects and would choke on a bare array). `node --test` runs each suite FILE in
// its own process, so each flushes reports/coverage/<pid>.json on exit; the gate
// (coverage_gate.py) unions the shards. A `pretest` step clears the dir before
// each run so a PRIOR run's shards can't inflate coverage into a false PASS.
const COVERAGE_DIR = fileURLToPath(new URL("../reports/coverage/", import.meta.url));

const covered = new Set<string>();
let installed = false;

export function recordRequest(method: string, pathname: string, status: number): void {
  if (status < 200 || status >= 300) return; // only a success proves the op ran
  covered.add(`${method.toUpperCase()} ${pathname}`);
  if (!installed) {
    installed = true;
    // Sync flush on 'exit' — the only hook guaranteed to run once the suite's
    // tests are done. NOTE: 'exit' does NOT fire on SIGTERM/SIGKILL, so a suite the
    // runner force-kills contributes no shard → its unique ops read UNCOVERED. That
    // is a safe false-FAIL (never a false-PASS), because the pretest clean removes
    // any stale shard that could otherwise mask the loss.
    process.on("exit", () => {
      if (covered.size === 0) return;
      try {
        mkdirSync(COVERAGE_DIR, { recursive: true });
        writeFileSync(`${COVERAGE_DIR}${process.pid}.json`, JSON.stringify([...covered]));
      } catch {
        /* best-effort: coverage is advisory, must never fail a suite */
      }
    });
  }
}
