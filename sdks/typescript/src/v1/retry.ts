// Retry + idempotency for the e2a SDK (Slice 8b-1).
//
// Implemented as a wrapping HttpLibrary rather than generated middleware: middleware
// can transform a request/response but cannot re-send, and re-sending is the
// whole point. Because we re-send the SAME RequestContext, the Idempotency-Key
// header and the already-serialized body are reused verbatim across attempts —
// which is exactly what makes a POST retry safe (the server dedupes on the key
// and hashes the raw bytes). The key is minted ONCE, before the first attempt.

import { from, Observable } from "./generated/rxjsStub.js";
import { HttpMethod } from "./generated/http/http.js";
import type { HttpLibrary, RequestContext, ResponseContext } from "./generated/http/http.js";
import { isRetryableStatus } from "./errors.js";

export interface RetryOptions {
  /** Max retry attempts after the first try. Default 2. */
  maxRetries?: number;
  /** Base backoff in ms (doubles per attempt). Default 200. */
  baseDelayMs?: number;
  /** Exponential-backoff ceiling in ms. Default 8000. */
  maxDelayMs?: number;
  /** Upper bound for an honored Retry-After, in ms. Default 60000 — a hostile
   *  or buggy upstream can otherwise wedge a request for years. */
  maxRetryAfterMs?: number;
  /** Optional total deadline across all attempts (incl. backoff), in ms. When
   *  the next sleep would exceed it, stop and return/throw the last result. */
  maxElapsedMs?: number;
  /** Injectable for tests. */
  sleep?: (ms: number) => Promise<void>;
  random?: () => number;
  genIdempotencyKey?: () => string;
  now?: () => number;
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

function abortError(reason?: unknown): unknown {
  if (reason !== undefined) return reason;
  return new DOMException("The operation was aborted", "AbortError");
}

function defaultUuid(): string {
  // Every supported runtime (Node 18+, browsers, edge) has crypto.randomUUID;
  // the fallback is a non-crypto last resort and is effectively unreachable.
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
    const now = this.opts.now ?? Date.now;
    const start = now();
    const signal = request.getSignal?.();

    let attempt = 0;
    for (;;) {
      this.throwIfAborted(signal);

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

      const delay = this.backoffMs(attempt, resp);
      // Total-deadline guard: don't start a wait that would blow the deadline.
      if (this.opts.maxElapsedMs !== undefined && now() - start + delay > this.opts.maxElapsedMs) {
        if (resp !== undefined) return resp;
        throw connErr;
      }
      await this.sleep(delay, signal);
      attempt += 1;
    }
  }

  // Mint an Idempotency-Key once, before the first attempt, for an unsafe
  // method the caller didn't already key. Setting it on the shared
  // RequestContext means every retry of this request reuses the same key.
  //
  // The generated layer unconditionally calls setHeaderParam("Idempotency-Key",
  // serialize(undefined)) on send/reply/forward/approve, leaving the header
  // *present but empty* when the caller passed no key. So "present" isn't enough
  // to mean "caller-supplied" — only a non-empty value counts; otherwise we mint
  // (overwriting the empty stub).
  private ensureIdempotencyKey(request: RequestContext): void {
    if (!UNSAFE_METHODS.has(request.getHttpMethod())) return;
    const headers = request.getHeaders();
    for (const k of Object.keys(headers)) {
      if (k.toLowerCase() !== IDEMPOTENCY_HEADER.toLowerCase()) continue;
      const v = headers[k];
      if (v != null && String(v).trim() !== "") return; // genuine caller-supplied key wins
      break; // present-but-empty generated stub → fall through and mint
    }
    request.setHeaderParam(IDEMPOTENCY_HEADER, (this.opts.genIdempotencyKey ?? defaultUuid)());
  }

  private throwIfAborted(signal: AbortSignal | undefined): void {
    if (signal?.aborted) throw abortError(signal.reason);
  }

  // Backoff: honor Retry-After (seconds or HTTP-date) up to maxRetryAfterMs;
  // otherwise exponential + jitter capped at maxDelayMs.
  private backoffMs(attempt: number, resp?: ResponseContext): number {
    const ra = resp?.headers ? this.retryAfterMs(resp.headers) : undefined;
    if (ra !== undefined) return Math.min(ra, this.opts.maxRetryAfterMs ?? 60000);

    const maxDelay = this.opts.maxDelayMs ?? 8000;
    const base = this.opts.baseDelayMs ?? 200;
    const exp = Math.min(base * 2 ** attempt, maxDelay);
    const rand = (this.opts.random ?? Math.random)();
    return Math.round(exp * (0.5 + 0.5 * rand)); // full jitter over [0.5, 1.0]
  }

  private retryAfterMs(headers: Record<string, string>): number | undefined {
    for (const k of Object.keys(headers)) {
      if (k.toLowerCase() !== "retry-after") continue;
      const v = headers[k];
      const secs = Number(v);
      if (Number.isFinite(secs) && secs >= 0) return secs * 1000;
      // RFC 9110 §10.2.3 allows an HTTP-date — common behind CDNs.
      const at = Date.parse(v);
      if (Number.isFinite(at)) return Math.max(0, at - (this.opts.now ?? Date.now)());
      return undefined;
    }
    return undefined;
  }

  private async sleep(ms: number, signal: AbortSignal | undefined): Promise<void> {
    // Injected sleep (tests) — still respect a pre-aborted signal.
    if (this.opts.sleep) {
      this.throwIfAborted(signal);
      return this.opts.sleep(ms);
    }
    if (!signal) return defaultSleep(ms);
    // Race the backoff against an abort so a cancelled request stops promptly.
    return new Promise<void>((resolve, reject) => {
      if (signal.aborted) {
        reject(abortError(signal.reason));
        return;
      }
      const t = setTimeout(() => {
        signal.removeEventListener("abort", onAbort);
        resolve();
      }, ms);
      const onAbort = () => {
        clearTimeout(t);
        reject(abortError(signal.reason));
      };
      signal.addEventListener("abort", onAbort, { once: true });
    });
  }
}
