import { test } from "node:test";
import assert from "node:assert/strict";
import { agentHeadroom } from "./account.ts";

test("agentHeadroom returns the unused agent capacity", () => {
  assert.equal(
    agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: 4 } }),
    6,
  );
});

test("agentHeadroom never returns a negative slot count", () => {
  assert.equal(
    agentHeadroom({ limits: { max_agents: 3 }, usage: { agents: 5 } }),
    0,
  );
});
