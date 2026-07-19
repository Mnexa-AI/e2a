import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, test } from "node:test";

import {
  AGENT_DOC_MIRRORS,
  parseArgs,
  syncAgentDocs,
} from "./sync-agent-docs.mjs";

const roots = [];

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true })));
});

async function fixture() {
  const repoRoot = await mkdtemp(join(tmpdir(), "e2a-agent-docs-"));
  roots.push(repoRoot);
  for (const [index, [source]] of AGENT_DOC_MIRRORS.entries()) {
    const sourcePath = join(repoRoot, source);
    await mkdir(join(sourcePath, ".."), { recursive: true });
    await writeFile(sourcePath, `canonical-${index}\n`);
  }
  return repoRoot;
}

test("sync creates byte-identical hosted mirrors and check accepts them", async () => {
  const repoRoot = await fixture();

  await syncAgentDocs({ repoRoot, check: false, log: () => {} });

  for (const [source, target] of AGENT_DOC_MIRRORS) {
    assert.deepEqual(
      await readFile(join(repoRoot, target)),
      await readFile(join(repoRoot, source)),
    );
  }
  await assert.doesNotReject(
    syncAgentDocs({ repoRoot, check: true, log: () => {} }),
  );
});

test("check reports every missing or stale hosted mirror without writing", async () => {
  const repoRoot = await fixture();
  const [, staleTarget] = AGENT_DOC_MIRRORS[1];
  await mkdir(join(repoRoot, staleTarget, ".."), { recursive: true });
  await writeFile(join(repoRoot, staleTarget), "stale\n");

  await assert.rejects(
    syncAgentDocs({ repoRoot, check: true, log: () => {} }),
    (error) => {
      assert.match(error.message, /missing hosted agent doc: web\/public\/e2a\.md/);
      assert.match(error.message, /stale hosted agent doc: web\/public\/templates\.md/);
      return true;
    },
  );
  const missingState = await readFile(join(repoRoot, AGENT_DOC_MIRRORS[0][1])).then(
    () => "present",
    () => "missing",
  );
  assert.equal(missingState, "missing");
  assert.equal(await readFile(join(repoRoot, staleTarget), "utf8"), "stale\n");
});

test("sync fails clearly when a canonical source is missing", async () => {
  const repoRoot = await fixture();
  await rm(join(repoRoot, AGENT_DOC_MIRRORS[0][0]));

  await assert.rejects(
    syncAgentDocs({ repoRoot, check: false, log: () => {} }),
    /missing canonical agent doc: plugins\/e2a\/docs\/e2a\.md/,
  );
});

test("parseArgs accepts check mode and rejects unknown options", () => {
  assert.deepEqual(parseArgs([]), { check: false });
  assert.deepEqual(parseArgs(["--check"]), { check: true });
  assert.throws(() => parseArgs(["--wat"]), /unknown option: --wat/);
  assert.throws(
    () => parseArgs(["--check", "extra"]),
    /usage: node scripts\/sync-agent-docs\.mjs \[--check\]/,
  );
});
