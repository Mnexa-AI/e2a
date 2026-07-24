import { afterEach, describe, expect, it, vi } from "vitest";
import { startHttpServer, type HttpServerOptions } from "../src/http-server.js";
import { MetricsRegistry } from "../src/metrics.js";
import type { McpClient } from "../src/client.js";

function makeStubClient(): McpClient {
  const stub = {
    agentEmail: "bot@example.com",
    scope: "account" as const,
    whoami: vi.fn(async () => ({ user: "owner@example.com", scope: "account", agentEmail: undefined })),
  };
  return stub as unknown as McpClient;
}

describe("GET /readyz", () => {
  let close: (() => Promise<void>) | undefined;

  afterEach(async () => {
    await close?.();
    close = undefined;
  });

  async function start(readyz?: HttpServerOptions["readyz"], metrics?: MetricsRegistry) {
    const started = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: () => makeStubClient(),
      logger: () => {},
      ...(readyz ? { readyz } : {}),
      ...(metrics ? { metrics } : {}),
    });
    close = started.close;
    return `http://127.0.0.1:${started.port}`;
  }

  it("returns 200 {ok:true} when the API health probe succeeds", async () => {
    const fetcher = vi.fn(async () => ({}));
    const origin = await start({ fetcher });
    const res = await fetch(`${origin}/readyz`);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true, checks: { api: "ok" } });
    expect(fetcher).toHaveBeenCalledWith("http://e2a.local/api/health");
    // Unauthenticated, like /healthz, but still correlated.
    expect(res.headers.get("x-request-id")).toMatch(/^mcpreq_[0-9a-f]{12}$/);
  });

  it("returns 503 + Retry-After when the probe fails, with request_id in the body", async () => {
    const fetcher = vi.fn(async () => {
      throw new Error("connection refused");
    });
    const origin = await start({ fetcher });
    const res = await fetch(`${origin}/readyz`, { headers: { "X-Request-Id": "readyz-check-1" } });
    expect(res.status).toBe(503);
    // Retry-After matches the failure-cache TTL (default 10s): retrying
    // sooner is guaranteed to hit the cached 503.
    expect(res.headers.get("retry-after")).toBe("10");
    expect(res.headers.get("x-request-id")).toBe("readyz-check-1");
    expect(await res.json()).toEqual({
      ok: false,
      checks: { api: "unreachable" },
      request_id: "readyz-check-1",
    });
  });

  it("treats a probe that never answers (past the timeout) as unreachable", async () => {
    // The deferred promise never settles, so the only way this request can
    // complete is the timeout path — deterministic, no fake clock needed.
    const fetcher = vi.fn(() => new Promise<unknown>(() => {}));
    const origin = await start({ fetcher, timeoutMs: 1 });
    const res = await fetch(`${origin}/readyz`);
    expect(res.status).toBe(503);
    expect((await res.json()).checks).toEqual({ api: "unreachable" });
  });

  it("caches the probe result for 10s (injectable clock)", async () => {
    let now = 0;
    const fetcher = vi.fn(async () => ({}));
    const origin = await start({ fetcher, now: () => now });

    const first = await fetch(`${origin}/readyz`);
    expect(first.status).toBe(200);
    const second = await fetch(`${origin}/readyz`);
    expect(second.status).toBe(200);
    // Two rapid requests share one probe.
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Just inside the TTL: still cached.
    now = 9_999;
    await fetch(`${origin}/readyz`);
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Past the TTL: re-probes.
    now = 10_001;
    const third = await fetch(`${origin}/readyz`);
    expect(third.status).toBe(200);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("caches failures too, then recovers after the TTL", async () => {
    let now = 0;
    let down = true;
    const fetcher = vi.fn(async () => {
      if (down) throw new Error("api down");
      return {};
    });
    const origin = await start({ fetcher, now: () => now });

    expect((await fetch(`${origin}/readyz`)).status).toBe(503);
    // A rapid retry serves the cached failure without re-probing.
    expect((await fetch(`${origin}/readyz`)).status).toBe(503);
    expect(fetcher).toHaveBeenCalledTimes(1);

    down = false;
    now = 10_001;
    expect((await fetch(`${origin}/readyz`)).status).toBe(200);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("increments mcp_readyz_checks_total by result", async () => {
    const registry = new MetricsRegistry();
    let down = true;
    let now = 0;
    const fetcher = vi.fn(async () => {
      if (down) throw new Error("api down");
      return {};
    });
    const origin = await start({ fetcher, now: () => now }, registry);

    await fetch(`${origin}/readyz`); // degraded
    down = false;
    now = 10_001;
    await fetch(`${origin}/readyz`); // ok

    const text = registry.render();
    expect(text).toContain('mcp_readyz_checks_total{result="degraded"} 1');
    expect(text).toContain('mcp_readyz_checks_total{result="ok"} 1');
  });

  it("/healthz stays pure liveness even when the API is down", async () => {
    const fetcher = vi.fn(async () => {
      throw new Error("api down");
    });
    const origin = await start({ fetcher });
    const res = await fetch(`${origin}/healthz`);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true });
    // Liveness never consults the probe.
    expect(fetcher).not.toHaveBeenCalled();
  });
});
