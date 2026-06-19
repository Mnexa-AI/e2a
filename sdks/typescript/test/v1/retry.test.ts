import { describe, it, expect } from "vitest";
import { RetryHttpLibrary } from "../../src/v1/retry.js";
import {
  HttpMethod,
  RequestContext,
  ResponseContext,
} from "../../src/v1/generated/http/http.js";
import type { HttpLibrary } from "../../src/v1/generated/http/http.js";
import { from, Observable } from "../../src/v1/generated/rxjsStub.js";

type Step = { status?: number; headers?: Record<string, string>; throw?: unknown };

// Records what each attempt saw (method + idempotency key) and replays a script.
class FakeHttp implements HttpLibrary {
  public seenKeys: (string | undefined)[] = [];
  public methods: HttpMethod[] = [];
  constructor(private steps: Step[]) {}
  send(req: RequestContext): Observable<ResponseContext> {
    this.methods.push(req.getHttpMethod());
    this.seenKeys.push(req.getHeaders()["Idempotency-Key"]);
    const step = this.steps.shift();
    if (!step) throw new Error("FakeHttp: no scripted step left");
    if (step.throw !== undefined) return from(Promise.reject(step.throw));
    // The retry layer never reads the body — a minimal stub suffices.
    const body = { binary: async () => new Blob([]), text: async () => "" } as never;
    return from(Promise.resolve(new ResponseContext(step.status!, step.headers ?? {}, body)));
  }
}

const noSleep = async () => {};
// A server-deduped POST (send/reply/forward/approve): the generated layer emits
// an empty Idempotency-Key stub, which marks the op safe to retry.
const post = () => {
  const r = new RequestContext("https://api.e2a.dev/v1/agents/a@x.com/messages", HttpMethod.POST);
  r.setHeaderParam("Idempotency-Key", ""); // empty generated stub
  return r;
};
// A bare, non-idempotent POST (create agent/domain/webhook, reject, redeliver,
// verify, test, rotate-secret): no server-honored key — must NOT be retried.
const barePost = (url = "https://api.e2a.dev/v1/agents") =>
  new RequestContext(url, HttpMethod.POST);
const get = () => new RequestContext("https://api.e2a.dev/v1/agents/a@x.com/messages", HttpMethod.GET);
const del = (url: string) => new RequestContext(url, HttpMethod.DELETE);
const patch = (url = "https://api.e2a.dev/v1/agents/a@x.com") =>
  new RequestContext(url, HttpMethod.PATCH);

describe("RetryHttpLibrary retry behavior", () => {
  it("retries a 500 then returns the 200", async () => {
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(post()).toPromise();
    expect(resp.httpStatusCode).toBe(200);
    expect(fake.methods.length).toBe(2);
  });

  it("retries a connection error then returns the 200", async () => {
    const fake = new FakeHttp([{ throw: new Error("ECONNREFUSED") }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(post()).toPromise();
    expect(resp.httpStatusCode).toBe(200);
    expect(fake.methods.length).toBe(2);
  });

  it("does NOT retry a non-retryable status (404)", async () => {
    const fake = new FakeHttp([{ status: 404 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(post()).toPromise();
    expect(resp.httpStatusCode).toBe(404);
    expect(fake.methods.length).toBe(1);
  });

  it("stops after maxRetries and returns the last response", async () => {
    const fake = new FakeHttp([{ status: 503 }, { status: 503 }, { status: 503 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, maxRetries: 2 });
    const resp = await retry.send(post()).toPromise();
    expect(resp.httpStatusCode).toBe(503);
    expect(fake.methods.length).toBe(3); // 1 + 2 retries
  });

  it("honors Retry-After for the backoff delay", async () => {
    const delays: number[] = [];
    const fake = new FakeHttp([{ status: 429, headers: { "retry-after": "12" } }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, {
      sleep: async (ms) => { delays.push(ms); },
      maxRetries: 1,
    });
    await retry.send(post()).toPromise();
    expect(delays).toEqual([12000]);
  });
});

describe("RetryHttpLibrary idempotency", () => {
  it("mints an Idempotency-Key ONCE and reuses it across retries", async () => {
    let n = 0;
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, genIdempotencyKey: () => `k-${n++}` });
    await retry.send(post()).toPromise();
    expect(fake.seenKeys[0]).toBe("k-0");
    expect(fake.seenKeys[1]).toBe("k-0"); // same key on the retry — not regenerated
    expect(n).toBe(1); // generated exactly once
  });

  it("does not mint a key for a safe method (GET)", async () => {
    const fake = new FakeHttp([{ status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    await retry.send(get()).toPromise();
    expect(fake.seenKeys[0]).toBeUndefined();
  });

  it("mints a key when the header is present but empty (generated-layer stub)", async () => {
    // The generated send/reply/forward/approve unconditionally set an empty
    // Idempotency-Key header when the caller passes none. A present-but-empty
    // header must NOT be mistaken for a caller-supplied key.
    const req = post();
    req.setHeaderParam("Idempotency-Key", "");
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, genIdempotencyKey: () => "minted" });
    await retry.send(req).toPromise();
    expect(fake.seenKeys).toEqual(["minted", "minted"]);
  });

  it("preserves a caller-supplied Idempotency-Key", async () => {
    const req = post();
    req.setHeaderParam("Idempotency-Key", "caller-key");
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, genIdempotencyKey: () => "should-not-be-used" });
    await retry.send(req).toPromise();
    expect(fake.seenKeys).toEqual(["caller-key", "caller-key"]);
  });
});

// Regression tests for the independent + adversarial review findings.
describe("RetryHttpLibrary review fixes", () => {
  it("aborting the signal stops the retry loop (no further attempt)", async () => {
    const ctl = new AbortController();
    const req = post();
    req.setSignal(ctl.signal);
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    // The injected backoff aborts; the next loop iteration must throw, not retry.
    const retry = new RetryHttpLibrary(fake, { sleep: async () => { ctl.abort(); }, maxRetries: 3 });
    await expect(retry.send(req).toPromise()).rejects.toBeDefined();
    expect(fake.methods.length).toBe(1); // only the first attempt ran
  });

  it("clamps an oversized Retry-After to maxRetryAfterMs", async () => {
    const delays: number[] = [];
    const fake = new FakeHttp([{ status: 503, headers: { "retry-after": "99999999" } }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, {
      sleep: async (ms) => { delays.push(ms); },
      maxRetries: 1,
      maxRetryAfterMs: 5000,
    });
    await retry.send(post()).toPromise();
    expect(delays).toEqual([5000]); // not ~3 years
  });

  it("honors an HTTP-date Retry-After", async () => {
    const FIXED = 1700000000000;
    const dateHeader = new Date(FIXED + 2000).toUTCString();
    const delays: number[] = [];
    const fake = new FakeHttp([{ status: 503, headers: { "retry-after": dateHeader } }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, {
      sleep: async (ms) => { delays.push(ms); },
      maxRetries: 1,
      now: () => FIXED,
    });
    await retry.send(post()).toPromise();
    expect(delays).toEqual([2000]);
  });

  it("stops once the total deadline (maxElapsedMs) would be exceeded", async () => {
    const fake = new FakeHttp([{ status: 503 }, { status: 503 }, { status: 503 }]);
    const retry = new RetryHttpLibrary(fake, {
      sleep: noSleep,
      random: () => 1, // max jitter → backoff(0) = 200ms
      now: () => 1000, // frozen clock: elapsed stays 0, but delay 200 > 100
      maxElapsedMs: 100,
      maxRetries: 5,
    });
    const resp = await retry.send(post()).toPromise();
    expect(resp.httpStatusCode).toBe(503);
    expect(fake.methods.length).toBe(1); // deadline blocked the first retry
  });
});

// Per-operation gating: only reads, HTTP-idempotent writes, and server-deduped
// (keyed) POSTs are retried. Non-idempotent POSTs and account deletion are not —
// retrying them after an ambiguous failure could double-create or re-fire an
// irreversible delete. Mirrors the Python SDK's executor gating.
describe("RetryHttpLibrary per-operation gating", () => {
  it("does NOT retry a bare (non-idempotent) POST create on 500", async () => {
    const fake = new FakeHttp([{ status: 500 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(barePost()).toPromise();
    expect(resp.httpStatusCode).toBe(500);
    expect(fake.methods.length).toBe(1); // not retried — avoids double-create
  });

  it("does NOT mint an Idempotency-Key for a bare POST create", async () => {
    const fake = new FakeHttp([{ status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, genIdempotencyKey: () => "x" });
    await retry.send(barePost()).toPromise();
    expect(fake.seenKeys[0]).toBeUndefined(); // no useless key the server would ignore
  });

  it("does NOT retry DELETE /v1/account (irreversible)", async () => {
    const fake = new FakeHttp([{ status: 500 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry
      .send(del("https://api.e2a.dev/v1/account?confirm=DELETE"))
      .toPromise();
    expect(resp.httpStatusCode).toBe(500);
    expect(fake.methods.length).toBe(1);
  });

  it("DOES retry an idempotent DELETE (webhook) on 500", async () => {
    const fake = new FakeHttp([{ status: 500 }, { status: 204 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(del("https://api.e2a.dev/v1/webhooks/wh_1")).toPromise();
    expect(resp.httpStatusCode).toBe(204);
    expect(fake.methods.length).toBe(2);
  });

  it("DOES retry an idempotent PATCH on 500", async () => {
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep });
    const resp = await retry.send(patch()).toPromise();
    expect(resp.httpStatusCode).toBe(200);
    expect(fake.methods.length).toBe(2);
  });
});
