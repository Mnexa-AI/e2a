import { createHash } from "node:crypto";
import type { Scope } from "./tools/tiers.js";

/**
 * The result of resolving a bearer's identity at the e2a backend: the
 * credential's tier-gating scope plus, for agent-scoped credentials, the
 * bound agent address. Derived from one `whoami` (GET /account) call.
 */
export interface ResolvedPrincipal {
  scope: Scope;
  /** The credential-bound agent for agent scope; absent for account scope. */
  agentEmail?: string;
}

/**
 * SHA-256 the bearer token. Used as the cache key so a memory dump of the
 * resolution cache doesn't carry cleartext credentials. Hex digest, 64 chars.
 */
export function fingerprintBearer(bearer: string): string {
  return createHash("sha256").update(bearer).digest("hex");
}

interface CacheEntry {
  value: ResolvedPrincipal;
  expiresAt: number;
}

export interface ResolveCacheOptions {
  /** How long a resolved principal stays cached. */
  ttlMs: number;
  /** Hard cap on cached principals; oldest is dropped past this. */
  maxEntries: number;
  /** Override for tests. Defaults to `Date.now`. */
  now?: () => number;
}

/**
 * Bearer → resolved-principal cache for the **stateless** MCP transport.
 *
 * The HTTP MCP server holds no per-connection session state (§4.2, stateless):
 * every request re-authenticates and dispatches independently, so a reaped
 * session can never drop a live connection the way the old in-memory session
 * map's 5-minute idle GC did. The only thing worth keeping between requests is
 * the `whoami` lookup that resolves a credential's scope + bound agent — a
 * backend round-trip we'd otherwise pay on *every* JSON-RPC message. This cache
 * collapses a burst of tool calls from one bearer into a single `whoami`.
 *
 * Crucially, this is a *cache*, not a session: an entry expiring (or being
 * evicted) triggers a re-resolve on the next request, never a disconnect. The
 * backend still authenticates the bearer on every actual API call, so a stale
 * scope can't widen privilege — it only saves a lookup.
 */
export class ResolveCache {
  private readonly map = new Map<string, CacheEntry>();
  private readonly opts: Required<ResolveCacheOptions>;

  constructor(opts: ResolveCacheOptions) {
    this.opts = { ...opts, now: opts.now ?? Date.now };
  }

  /** Return the live (unexpired) resolution for a bearer, or undefined. */
  get(bearer: string): ResolvedPrincipal | undefined {
    const key = fingerprintBearer(bearer);
    const entry = this.map.get(key);
    if (!entry) return undefined;
    if (entry.expiresAt <= this.opts.now()) {
      this.map.delete(key);
      return undefined;
    }
    return entry.value;
  }

  /** Cache a resolution for `ttlMs`. Evicts the oldest entry past the cap. */
  set(bearer: string, value: ResolvedPrincipal): void {
    const key = fingerprintBearer(bearer);
    if (!this.map.has(key) && this.map.size >= this.opts.maxEntries) {
      // Map preserves insertion order, so the first key is the oldest.
      const oldest = this.map.keys().next().value;
      if (oldest !== undefined) this.map.delete(oldest);
    }
    this.map.set(key, { value, expiresAt: this.opts.now() + this.opts.ttlMs });
  }

  size(): number {
    return this.map.size;
  }

  clear(): void {
    this.map.clear();
  }
}
