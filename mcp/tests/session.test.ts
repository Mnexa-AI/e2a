import { describe, expect, it, vi } from "vitest";
import { Sessions, type SessionEntry } from "../src/session.js";

// Fake transport/server pair — the session map only calls transport.close().
function fakeEntry(lastSeen = Date.now()): SessionEntry {
  return {
    transport: { close: vi.fn(async () => undefined) } as unknown as SessionEntry["transport"],
    server: {} as SessionEntry["server"],
    lastSeen,
  };
}

describe("Sessions", () => {
  it("put + get returns the entry and bumps lastSeen", () => {
    let t = 1_000;
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10, now: () => t });
    const entry = fakeEntry(t);
    sessions.put("a", entry);
    t = 2_000;
    const got = sessions.get("a");
    expect(got).toBe(entry);
    expect(got!.lastSeen).toBe(2_000);
  });

  it("get returns undefined for unknown id", () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10 });
    expect(sessions.get("nope")).toBeUndefined();
  });

  it("delete closes the transport and removes the entry", async () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10 });
    const entry = fakeEntry();
    sessions.put("a", entry);
    await sessions.delete("a");
    expect(entry.transport.close).toHaveBeenCalledOnce();
    expect(sessions.get("a")).toBeUndefined();
  });

  it("delete is a no-op for unknown id", async () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10 });
    await expect(sessions.delete("nope")).resolves.toBeUndefined();
  });

  it("LRU evicts the oldest entry when at capacity", async () => {
    let t = 0;
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 2, now: () => t });
    const a = fakeEntry(0);
    const b = fakeEntry(10);
    const c = fakeEntry(20);
    sessions.put("a", a);
    sessions.put("b", b);
    t = 30;
    sessions.put("c", c); // should evict a (oldest lastSeen)
    // delete is fire-and-forget inside evictOldest; wait a tick
    await new Promise((r) => setImmediate(r));
    expect(a.transport.close).toHaveBeenCalledOnce();
    expect(sessions.get("a")).toBeUndefined();
    expect(sessions.get("b")).toBeDefined();
    expect(sessions.get("c")).toBeDefined();
  });

  it("LRU eviction tolerates a close() that rejects", async () => {
    let t = 0;
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 1, now: () => t });
    const stderr = vi.spyOn(process.stderr, "write").mockReturnValue(true);
    const bad = {
      transport: { close: vi.fn(async () => { throw new Error("boom"); }) },
      server: {},
      lastSeen: 0,
    } as unknown as SessionEntry;
    sessions.put("bad", bad);
    t = 10;
    // Evicting "bad" fire-and-forgets a delete() whose close() rejects; this
    // must not surface as an unhandled rejection, and the entry is still gone.
    sessions.put("good", fakeEntry(10));
    await new Promise((r) => setImmediate(r));
    expect(bad.transport.close).toHaveBeenCalledOnce();
    expect(sessions.get("bad")).toBeUndefined();
    expect(sessions.get("good")).toBeDefined();
    expect(stderr).toHaveBeenCalledWith(expect.stringContaining("session evict close error: boom"));
    stderr.mockRestore();
  });

  it("re-putting an existing id does not trigger eviction", () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 1 });
    const a1 = fakeEntry();
    const a2 = fakeEntry();
    sessions.put("a", a1);
    sessions.put("a", a2); // overwrite, not evict
    expect(a1.transport.close).not.toHaveBeenCalled();
    expect(sessions.get("a")).toBe(a2);
  });

  it("gc reaps entries older than idleTimeoutMs", async () => {
    let t = 0;
    const sessions = new Sessions({ idleTimeoutMs: 1_000, maxSessions: 10, now: () => t });
    const stale = fakeEntry(0);
    const fresh = fakeEntry(1_500); // newer than cutoff (now - idle = 1000)
    sessions.put("stale", stale);
    sessions.put("fresh", fresh);
    t = 2_000;
    await sessions.gc();
    expect(stale.transport.close).toHaveBeenCalledOnce();
    expect(fresh.transport.close).not.toHaveBeenCalled();
    expect(sessions.size()).toBe(1);
  });

  it("shutdown closes all transports and is idempotent", async () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10 });
    const a = fakeEntry();
    const b = fakeEntry();
    sessions.put("a", a);
    sessions.put("b", b);
    await sessions.shutdown();
    expect(a.transport.close).toHaveBeenCalled();
    expect(b.transport.close).toHaveBeenCalled();
    expect(sessions.size()).toBe(0);
    await expect(sessions.shutdown()).resolves.toBeUndefined();
  });

  it("put after shutdown is rejected", async () => {
    const sessions = new Sessions({ idleTimeoutMs: 60_000, maxSessions: 10 });
    await sessions.shutdown();
    sessions.put("a", fakeEntry());
    expect(sessions.size()).toBe(0);
  });

  it("gc reaps remaining entries even when one close() throws", async () => {
    let t = 0;
    const sessions = new Sessions({ idleTimeoutMs: 100, maxSessions: 10, now: () => t });
    const bad = {
      transport: { close: vi.fn(async () => { throw new Error("boom"); }) },
      server: {},
      lastSeen: 0,
    } as unknown as SessionEntry;
    const ok = fakeEntry(0);
    sessions.put("bad", bad);
    sessions.put("ok", ok);
    t = 1_000;
    // Should not throw; both stale entries should be removed from the map.
    await sessions.gc();
    expect(bad.transport.close).toHaveBeenCalledOnce();
    expect(ok.transport.close).toHaveBeenCalledOnce();
    expect(sessions.size()).toBe(0);
  });

  it("startGc + manual gc combined leaves no side effects", async () => {
    let t = 0;
    const sessions = new Sessions({ idleTimeoutMs: 100, maxSessions: 10, now: () => t });
    sessions.startGc(10_000); // long interval so the auto-tick doesn't race the test
    sessions.put("a", fakeEntry(0));
    t = 1_000;
    await sessions.gc();
    expect(sessions.size()).toBe(0);
    await sessions.shutdown();
  });
});
