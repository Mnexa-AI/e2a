import { describe, it, expect } from "vitest";
import { RetryHttpLibrary } from "../../src/v1/retry.js";
import {
  HttpMethod,
  RequestContext,
  ResponseContext,
} from "../../src/v1/oag/http/http.js";
import type { HttpLibrary } from "../../src/v1/oag/http/http.js";
import { from, Observable } from "../../src/v1/oag/rxjsStub.js";

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
const post = () => new RequestContext("https://api.e2a.dev/v1/agents/a@x.com/messages", HttpMethod.POST);
const get = () => new RequestContext("https://api.e2a.dev/v1/agents/a@x.com/messages", HttpMethod.GET);

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

  it("preserves a caller-supplied Idempotency-Key", async () => {
    const req = post();
    req.setHeaderParam("Idempotency-Key", "caller-key");
    const fake = new FakeHttp([{ status: 500 }, { status: 200 }]);
    const retry = new RetryHttpLibrary(fake, { sleep: noSleep, genIdempotencyKey: () => "should-not-be-used" });
    await retry.send(req).toPromise();
    expect(fake.seenKeys).toEqual(["caller-key", "caller-key"]);
  });
});
