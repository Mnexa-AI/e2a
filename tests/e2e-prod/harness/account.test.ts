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

test("agentHeadroom returns zero when capacity fields are missing", () => {
  assert.equal(agentHeadroom({}), 0);
  assert.equal(agentHeadroom({ limits: {}, usage: {} }), 0);
});

test("agentHeadroom returns zero for non-numeric capacity fields", () => {
  assert.equal(agentHeadroom({ limits: { max_agents: "10" }, usage: { agents: 4 } }), 0);
  assert.equal(agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: Number.NaN } }), 0);
});

test("agentHeadroom returns zero for negative or non-integer capacity fields", () => {
  assert.equal(agentHeadroom({ limits: { max_agents: -1 }, usage: { agents: 0 } }), 0);
  assert.equal(agentHeadroom({ limits: { max_agents: 10.5 }, usage: { agents: 4 } }), 0);
  assert.equal(agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: -1 } }), 0);
  assert.equal(agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: 4.5 } }), 0);
});
