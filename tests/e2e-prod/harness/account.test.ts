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

test("agentHeadroom rejects missing capacity fields", () => {
  assert.throws(() => agentHeadroom({}), /max_agents|agents/);
  assert.throws(() => agentHeadroom({ limits: {}, usage: {} }), /max_agents|agents/);
});

test("agentHeadroom rejects non-numeric capacity fields", () => {
  assert.throws(() => agentHeadroom({ limits: { max_agents: "10" }, usage: { agents: 4 } }), /max_agents/);
  assert.throws(() => agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: Number.NaN } }), /usage\.agents/);
});

test("agentHeadroom rejects negative or non-integer capacity fields", () => {
  assert.throws(() => agentHeadroom({ limits: { max_agents: -1 }, usage: { agents: 0 } }), /max_agents/);
  assert.throws(() => agentHeadroom({ limits: { max_agents: 10.5 }, usage: { agents: 4 } }), /max_agents/);
  assert.throws(() => agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: -1 } }), /usage\.agents/);
  assert.throws(() => agentHeadroom({ limits: { max_agents: 10 }, usage: { agents: 4.5 } }), /usage\.agents/);
});
