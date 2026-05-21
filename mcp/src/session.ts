import { createHash } from "node:crypto";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

export interface SessionEntry {
  transport: StreamableHTTPServerTransport;
  server: McpServer;
  lastSeen: number;
  /**
   * SHA-256 of the bearer token used at session initialize. Subsequent
   * requests targeting this session MUST present the same bearer; the
   * MCP server compares fingerprints in handleClientRequest before
   * dispatching to the session's transport. Without this binding a
   * leaked `Mcp-Session-Id` (CORS-exposed via Access-Control-Expose-
   * Headers; visible in logs / devtools / proxies) would let any
   * caller with any non-empty bearer act as the session's owner —
   * the per-session E2AClient holds the original bearer baked in and
   * forwards it verbatim to the backend, so the privilege check the
   * bearer-presence gate did upstream is meaningless to the dispatch.
   */
  bearerFingerprint: string;
}

/**
 * SHA-256 the bearer token. Used as the binding key for session
 * ↔ bearer pairing — we store this on SessionEntry instead of the
 * raw bearer so a memory dump of the sessions map doesn't carry
 * cleartext credentials. Hex digest, 64 chars.
 */
export function fingerprintBearer(bearer: string): string {
  return createHash("sha256").update(bearer).digest("hex");
}

export interface SessionsOptions {
  idleTimeoutMs: number;
  maxSessions: number;
  /** Override for tests. Defaults to `Date.now`. */
  now?: () => number;
}

/**
 * In-memory map of active MCP HTTP sessions.
 *
 * Per the design (v0.2.0 §4.2): sessions are short-lived state held only
 * for the duration of an active client connection. GC reaps idle entries
 * every 60s; an LRU eviction kicks in when `maxSessions` is reached so a
 * stuck container can't accumulate sessions indefinitely.
 */
export class Sessions {
  private readonly map = new Map<string, SessionEntry>();
  private readonly opts: Required<SessionsOptions>;
  private gcTimer?: ReturnType<typeof setInterval>;
  private shuttingDown = false;

  constructor(opts: SessionsOptions) {
    this.opts = { ...opts, now: opts.now ?? Date.now };
  }

  /** Start the idle-GC loop. Safe to call once at boot. */
  startGc(intervalMs = 60_000): void {
    if (this.gcTimer || this.shuttingDown) return;
    this.gcTimer = setInterval(() => this.gc(), intervalMs);
    // Don't keep the event loop alive just for GC.
    this.gcTimer.unref?.();
  }

  put(id: string, entry: SessionEntry): void {
    if (this.shuttingDown) return;
    if (this.map.size >= this.opts.maxSessions && !this.map.has(id)) {
      this.evictOldest();
    }
    this.map.set(id, entry);
  }

  /** Look up and bump lastSeen. */
  get(id: string): SessionEntry | undefined {
    const entry = this.map.get(id);
    if (entry) entry.lastSeen = this.opts.now();
    return entry;
  }

  /** Close the transport and remove from the map. Idempotent. */
  async delete(id: string): Promise<void> {
    const entry = this.map.get(id);
    if (!entry) return;
    this.map.delete(id);
    await entry.transport.close();
  }

  size(): number {
    return this.map.size;
  }

  /**
   * Sweep entries whose `lastSeen` is older than `idleTimeoutMs`. Runs on
   * the GC timer; safe to call manually in tests. Uses allSettled so a
   * single misbehaving transport.close() doesn't abort the whole sweep
   * and leave the other stale entries unreaped until the next tick.
   */
  async gc(): Promise<void> {
    if (this.shuttingDown) return;
    const cutoff = this.opts.now() - this.opts.idleTimeoutMs;
    const stale: string[] = [];
    for (const [id, entry] of this.map) {
      if (entry.lastSeen < cutoff) stale.push(id);
    }
    await Promise.allSettled(stale.map((id) => this.delete(id)));
  }

  /**
   * Close every session and stop accepting new ones. Idempotent.
   * Returns once all transports have closed (or rejected — we swallow
   * those because we're tearing down anyway).
   */
  async shutdown(): Promise<void> {
    if (this.shuttingDown) return;
    this.shuttingDown = true;
    if (this.gcTimer) clearInterval(this.gcTimer);
    this.gcTimer = undefined;
    const transports = [...this.map.values()].map((e) => e.transport);
    this.map.clear();
    await Promise.allSettled(transports.map((t) => t.close()));
  }

  private evictOldest(): void {
    let oldestId: string | undefined;
    let oldestSeen = Infinity;
    for (const [id, entry] of this.map) {
      if (entry.lastSeen < oldestSeen) {
        oldestSeen = entry.lastSeen;
        oldestId = id;
      }
    }
    if (oldestId) {
      // Fire-and-forget; the LRU eviction shouldn't block put().
      void this.delete(oldestId);
    }
  }
}
