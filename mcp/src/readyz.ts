const DEFAULT_TIMEOUT_MS = 2_000;
const DEFAULT_CACHE_TTL_MS = 10_000;

export interface ReadyzOptions {
  /** Probe timeout (ms). Default 2000. */
  timeoutMs?: number;
  /** How long a probe result (ok OR degraded) is cached (ms). Default 10000. */
  cacheTtlMs?: number;
  /** Test seam: clock override. Defaults to Date.now. */
  now?: () => number;
  /** Test seam: probe implementation. Resolves = reachable, rejects/never
   *  settles past the timeout = unreachable. */
  fetcher?: (url: string) => Promise<unknown>;
}

// defaultFetcher treats any non-2xx (or a network/abort failure) as
// unreachable; the AbortSignal bounds the probe so a hung connection can't
// outlive the timeout by much.
async function defaultFetcher(url: string, timeoutMs: number): Promise<unknown> {
  const res = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
  if (!res.ok) throw new Error(`api health probe returned ${res.status}`);
  return res;
}

/**
 * Readiness prober for `GET /readyz`: checks `{baseUrl}/api/health` with a
 * bounded timeout and caches the outcome (success AND failure) for
 * `cacheTtlMs` so a scraping fleet can't fan out into a probe storm against
 * an already-struggling backend. Pure liveness (`/healthz`) never touches
 * this.
 */
export class ReadinessProber {
  private cached: { ok: boolean; expiresAt: number } | undefined;
  private readonly now: () => number;
  private readonly timeoutMs: number;
  private readonly cacheTtlMs: number;
  private readonly fetcher: (url: string) => Promise<unknown>;

  constructor(private readonly baseUrl: string, opts: ReadyzOptions = {}) {
    this.now = opts.now ?? Date.now;
    this.timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.cacheTtlMs = opts.cacheTtlMs ?? DEFAULT_CACHE_TTL_MS;
    this.fetcher = opts.fetcher ?? ((url) => defaultFetcher(url, this.timeoutMs));
  }

  /**
   * The cache TTL in whole seconds — what a 503's Retry-After should say.
   * A shorter value would invite retries into a still-cached failure.
   */
  get cacheTtlSeconds(): number {
    return Math.max(1, Math.ceil(this.cacheTtlMs / 1000));
  }

  /** true when the backend API is reachable (possibly from the cache). */
  async check(): Promise<boolean> {
    const now = this.now();
    if (this.cached && this.cached.expiresAt > now) return this.cached.ok;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let ok = false;
    try {
      await Promise.race([
        this.fetcher(`${this.baseUrl}/api/health`),
        new Promise<never>((_resolve, reject) => {
          timer = setTimeout(
            () => reject(new Error(`readyz probe timed out after ${this.timeoutMs}ms`)),
            this.timeoutMs,
          );
        }),
      ]);
      ok = true;
    } catch {
      ok = false;
    } finally {
      if (timer) clearTimeout(timer);
    }
    this.cached = { ok, expiresAt: now + this.cacheTtlMs };
    return ok;
  }
}
