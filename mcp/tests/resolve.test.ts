import { describe, expect, it } from "vitest";
import { ResolveCache, fingerprintBearer, type ResolvedPrincipal } from "../src/resolve.js";

const PRIN: ResolvedPrincipal = { scope: "agent", agentEmail: "bot@example.com" };

describe("fingerprintBearer", () => {
  it("is deterministic and a 64-char hex digest", () => {
    const a = fingerprintBearer("ate2a_secret");
    expect(a).toMatch(/^[0-9a-f]{64}$/);
    expect(fingerprintBearer("ate2a_secret")).toBe(a);
  });

  it("maps distinct bearers to distinct fingerprints", () => {
    expect(fingerprintBearer("token_a")).not.toBe(fingerprintBearer("token_b"));
  });
});

describe("ResolveCache", () => {
  it("returns undefined on a miss", () => {
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 10 });
    expect(c.get("nope")).toBeUndefined();
  });

  it("returns the cached principal within the TTL", () => {
    let t = 0;
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 10, now: () => t });
    c.set("tok", PRIN);
    t = 999;
    expect(c.get("tok")).toEqual(PRIN);
  });

  it("expires the entry once the TTL elapses (and evicts it lazily)", () => {
    let t = 0;
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 10, now: () => t });
    c.set("tok", PRIN);
    t = 1000; // expiresAt is exclusive: now >= expiresAt ⇒ expired
    expect(c.get("tok")).toBeUndefined();
    expect(c.size()).toBe(0);
  });

  it("keys entries by bearer — distinct bearers don't collide", () => {
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 10 });
    c.set("a", { scope: "agent", agentEmail: "a@x" });
    c.set("b", { scope: "account" });
    expect(c.get("a")).toEqual({ scope: "agent", agentEmail: "a@x" });
    expect(c.get("b")).toEqual({ scope: "account" });
  });

  it("evicts the oldest entry when maxEntries is exceeded", () => {
    const c = new ResolveCache({ ttlMs: 60_000, maxEntries: 2 });
    c.set("a", PRIN);
    c.set("b", PRIN);
    c.set("c", PRIN); // over cap → drops "a" (oldest inserted)
    expect(c.size()).toBe(2);
    expect(c.get("a")).toBeUndefined();
    expect(c.get("b")).toEqual(PRIN);
    expect(c.get("c")).toEqual(PRIN);
  });

  it("re-setting an existing bearer updates the value and resets the TTL without evicting", () => {
    let t = 0;
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 1, now: () => t });
    c.set("tok", { scope: "agent", agentEmail: "old@x" });
    t = 500;
    c.set("tok", { scope: "account" }); // same key: overwrite, not evict
    expect(c.size()).toBe(1);
    expect(c.get("tok")).toEqual({ scope: "account" });
    t = 1499; // 500 + 1000 - 1: still live because the re-set reset the clock
    expect(c.get("tok")).toEqual({ scope: "account" });
    t = 1500;
    expect(c.get("tok")).toBeUndefined();
  });

  it("clear() empties the cache", () => {
    const c = new ResolveCache({ ttlMs: 1000, maxEntries: 10 });
    c.set("a", PRIN);
    c.set("b", PRIN);
    c.clear();
    expect(c.size()).toBe(0);
    expect(c.get("a")).toBeUndefined();
  });
});
