import { describe, it, expect } from "vitest";
import { AutoPager, type Page } from "../../src/v1/pagination.js";

// Build a 3-page fetcher: cursors page1→page2→null.
function pages(): (c: string | undefined) => Promise<Page<number>> {
  const data: Record<string, Page<number>> = {
    __start__: { items: [1, 2], next_cursor: "c2" },
    c2: { items: [3, 4], next_cursor: "c3" },
    c3: { items: [5], next_cursor: null },
  };
  return async (cursor) => data[cursor ?? "__start__"];
}

describe("AutoPager", () => {
  it("iterates every item across pages, stopping on null cursor", async () => {
    const pager = new AutoPager(pages());
    const out: number[] = [];
    for await (const n of pager) out.push(n);
    expect(out).toEqual([1, 2, 3, 4, 5]);
  });

  it("toArray respects the limit (stops mid-stream)", async () => {
    const pager = new AutoPager(pages());
    expect(await pager.toArray({ limit: 3 })).toEqual([1, 2, 3]);
  });

  it("toArray requires a positive limit", async () => {
    const pager = new AutoPager(pages());
    await expect(pager.toArray({ limit: 0 })).rejects.toThrow(/positive limit/);
  });

  it("forEach stops early when fn returns false", async () => {
    const pager = new AutoPager(pages());
    const out: number[] = [];
    await pager.forEach((n) => {
      out.push(n);
      return n < 3; // stop once we hit 3
    });
    expect(out).toEqual([1, 2, 3]);
  });

  it("aborts on a non-advancing cursor (no infinite loop)", async () => {
    let calls = 0;
    const pager = new AutoPager<number>(async () => {
      calls++;
      return { items: [calls], next_cursor: "stuck" }; // cursor never changes
    });
    const run = async () => {
      for await (const _ of pager) {
        if (calls > 10) throw new Error("looped");
      }
    };
    await expect(run()).rejects.toThrow(/did not advance/);
    expect(calls).toBeLessThanOrEqual(2);
  });

  it("an empty-string cursor terminates", async () => {
    const pager = new AutoPager<number>(async () => ({ items: [7], next_cursor: "" }));
    const out = await pager.toArray({ limit: 100 });
    expect(out).toEqual([7]);
  });

  // Adversarial review finding: a multi-step cycle (A-B-C-A-B-C…) slips past a
  // single one-step-back guard. The seen-set must catch any repeated cursor.
  it("aborts a multi-step cursor cycle (A-B-C-A)", async () => {
    const ring = ["c1", "c2", "c3"]; // start → c1 → c2 → c3 → c1 → …
    let i = 0;
    const pager = new AutoPager<number>(async () => {
      const next = ring[i % ring.length];
      i++;
      return { items: [i], next_cursor: next };
    });
    const run = async () => {
      for await (const _ of pager) {
        if (i > 20) throw new Error("looped");
      }
    };
    await expect(run()).rejects.toThrow(/did not advance/);
    expect(i).toBeLessThanOrEqual(5); // caught within one full cycle
  });

  // Adversarial finding: a never-repeating, never-null cursor defeats the
  // repeated-cursor guard; the page-count ceiling must backstop it.
  it("aborts an ever-advancing cursor at maxPages", async () => {
    let n = 0;
    const pager = new AutoPager<number>(async () => ({ items: [n], next_cursor: `c${++n}` }), {
      maxPages: 5,
    });
    const run = async () => {
      for await (const _ of pager) { /* drain */ }
    };
    await expect(run()).rejects.toThrow(/exceeded 5 pages/);
  });
});
