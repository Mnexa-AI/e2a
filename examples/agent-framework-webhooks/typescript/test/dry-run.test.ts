import { describe, expect, it } from "vitest";

import { runDryRun } from "../src/dry-run.js";

describe("runDryRun", () => {
  it("verifies, fetches, replies once, and deduplicates without provider keys", async () => {
    const evidence = await runDryRun({ print: false });
    expect(evidence).toEqual({
      firstStatus: "replied",
      secondStatus: "duplicate",
      reply: "Deterministic fake reply",
      replyCount: 1,
    });
  });
});
