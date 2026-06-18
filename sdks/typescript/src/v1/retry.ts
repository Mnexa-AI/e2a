// Retry + idempotency for the e2a SDK (Slice 8b-1).
//
// Implemented as a wrapping HttpLibrary rather than oag middleware: middleware
// can transform a request/response but cannot re-send, and re-sending is the
// whole point. Because we re-send the SAME RequestContext, the Idempotency-Key
// header and the already-serialized body are reused verbatim across attempts —
// which is exactly what makes a POST retry safe (the server dedupes on the key
// and hashes the raw bytes). The key is minted ONCE, before the first attempt.

import { from, Observable } from "./oag/rxjsStub.js";
import { HttpMethod } from "./oag/http/http.js";
import type { HttpLibrary, RequestContext, ResponseContext } from "./oag/http/http.js";
import { isRetryableStatus } from "./errors.js";

export interface RetryOptions {
  /** Max retry attempts after the first try. Default 2. */
  maxRetries?: number;
  /** Base backoff in ms (doubles per attempt). Default 200. */
  baseDelayMs?: number;
  /** Backoff ceiling in ms. Default 8000. */
  maxDelayMs?: number;
  /** Injectable for tests. */
  sleep?: (ms: number) => Promise<void>;
  random?: () => number;
  genIdempotencyKey?: () => string;
}

// Unsafe methods get an auto idempotency key when the caller didn't supply one.
const UNSAFE_METHODS = new Set<HttpMethod>([
  HttpMethod.POST,
  HttpMethod.PATCH,
  HttpMethod.PUT,
  HttpMethod.DELETE,
]);

const IDEMPOTENCY_HEADER = "Idempotency-Key";

function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function defaultUuid(): string {
  const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
  if (c?.randomUUID) return c.randomUUID();
  return `${Date.now().toString(16)}-${Math.random().toString(16).slice(2)}${Math.random().toString(16).slice(2)}`;
}

export class RetryHttpLibrary implements HttpLibrary {
  constructor(
    private readonly inner: HttpLibrary,
    private readonly opts: RetryOptions = {},
  ) {}

  send(request: RequestContext): Observable<ResponseContext> {
    return from(this.sendWithRetry(request));
  }

  private async sendWithRetry(request: RequestContext): Promise<ResponseContext> {
    this.ensureIdempotencyKey(request);
    const max = this.opts.maxRetries ?? 2;
    const sleep = this.opts.sleep ?? defaultSleep;

    let attempt = 0;
    for (;;) {
      let resp: ResponseContext | undefined;
      let connErr: unknown;
      try {
        resp = await this.inner.send(request).toPromise();
      } catch (e) {
        connErr = e; // connection-level failure (no HTTP response)
      }

      const isConn = resp === undefined;
      const retryable = isConn || isRetryableStatus(resp!.httpStatusCode);
      if (!retryable || attempt >= max) {
        if (resp !== undefined) return resp;
        throw connErr;
      }

      await sleep(this.backoffMs(attempt, resp));
      attempt += 1;
    }
  }

  // Mint an Idempotency-Key once, before the first attempt, for an unsafe
  // method the caller didn't already key. Setting it on the shared
  // RequestContext means every retry of this request reuses the same key.
  private ensureIdempotencyKey(request: RequestContext): void {
    if (!UNSAFE_METHODS.has(request.getHttpMethod())) return;
    const headers = request.getHeaders();
    for (const k of Object.keys(headers)) {
      if (k.toLowerCase() === IDEMPOTENCY_HEADER.toLowerCase()) return; // caller-supplied wins
    }
    request.setHeaderParam(IDEMPOTENCY_HEADER, (this.opts.genIdempotencyKey ?? defaultUuid)());
  }

  private backoffMs(attempt: number, resp?: ResponseContext): number {
    // Honor a server Retry-After (seconds) verbatim — the server is telling us
    // exactly when to retry; clamping it to the exponential ceiling would risk
    // retrying early into another 429. Only the exponential path is capped.
    const ra = resp?.headers ? this.retryAfterMs(resp.headers) : undefined;
    if (ra !== undefined) return ra;

    const maxDelay = this.opts.maxDelayMs ?? 8000;
    const base = this.opts.baseDelayMs ?? 200;
    const exp = Math.min(base * 2 ** attempt, maxDelay);
    // Full jitter over [0.5, 1.0] of the exponential window.
    const rand = (this.opts.random ?? Math.random)();
    return Math.round(exp * (0.5 + 0.5 * rand));
  }

  private retryAfterMs(headers: Record<string, string>): number | undefined {
    for (const k of Object.keys(headers)) {
      if (k.toLowerCase() === "retry-after") {
        const secs = Number(headers[k]);
        if (Number.isFinite(secs) && secs >= 0) return secs * 1000;
      }
    }
    return undefined;
  }
}
