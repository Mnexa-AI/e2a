import { afterEach, describe, expect, it, vi } from "vitest";
import { MetricsRegistry } from "../src/metrics.js";
import { startHttpServer } from "../src/http-server.js";
import type { McpClient } from "../src/client.js";

function makeStubClient(): McpClient {
  const stub = {
    agentEmail: "bot@example.com",
    scope: "account" as const,
    whoami: vi.fn(async () => ({ user: "owner@example.com", scope: "account", agentEmail: undefined })),
  };
  return stub as unknown as McpClient;
}

describe("MetricsRegistry", () => {
  it("renders HTTP request counters in Prometheus exposition format", () => {
    const m = new MetricsRegistry();
    m.incHttpRequest("mcp", "2xx");
    m.incHttpRequest("mcp", "2xx");
    m.incHttpRequest("healthz", "2xx");
    m.incHttpRequest("mcp", "4xx");
    const text = m.render();
    expect(text).toContain("# HELP mcp_http_requests_total ");
    expect(text).toContain("# TYPE mcp_http_requests_total counter");
    expect(text).toContain('mcp_http_requests_total{route="mcp",status_class="2xx"} 2');
    expect(text).toContain('mcp_http_requests_total{route="healthz",status_class="2xx"} 1');
    expect(text).toContain('mcp_http_requests_total{route="mcp",status_class="4xx"} 1');
  });

  it("tracks auth resolutions by result", () => {
    const m = new MetricsRegistry();
    m.incAuthResolution("cache_hit");
    m.incAuthResolution("resolved");
    m.incAuthResolution("invalid");
    m.incAuthResolution("fallback");
    const text = m.render();
    expect(text).toContain("# TYPE mcp_auth_resolutions_total counter");
    expect(text).toContain('mcp_auth_resolutions_total{result="cache_hit"} 1');
    expect(text).toContain('mcp_auth_resolutions_total{result="resolved"} 1');
    expect(text).toContain('mcp_auth_resolutions_total{result="invalid"} 1');
    expect(text).toContain('mcp_auth_resolutions_total{result="fallback"} 1');
  });

  it("tracks tool executions by tool and outcome", () => {
    const m = new MetricsRegistry();
    m.incToolExecution("list_agents", "ok");
    m.incToolExecution("send_message", "error");
    const text = m.render();
    expect(text).toContain("# TYPE mcp_tool_executions_total counter");
    expect(text).toContain('mcp_tool_executions_total{outcome="ok",tool="list_agents"} 1');
    expect(text).toContain('mcp_tool_executions_total{outcome="error",tool="send_message"} 1');
  });

  it("tracks readiness checks by result", () => {
    const m = new MetricsRegistry();
    m.incReadyzCheck("ok");
    m.incReadyzCheck("degraded");
    m.incReadyzCheck("degraded");
    const text = m.render();
    expect(text).toContain("# TYPE mcp_readyz_checks_total counter");
    expect(text).toContain('mcp_readyz_checks_total{result="ok"} 1');
    expect(text).toContain('mcp_readyz_checks_total{result="degraded"} 2');
  });

  it("observes request durations into a fixed-bucket histogram", () => {
    const m = new MetricsRegistry();
    m.observeRequestDuration("mcp", 0.04);
    m.observeRequestDuration("mcp", 0.6);
    const text = m.render();
    expect(text).toContain("# TYPE mcp_http_request_duration_seconds histogram");
    // 0.04 lands in the 0.05 bucket; both land in every wider bucket.
    expect(text).toContain('mcp_http_request_duration_seconds_bucket{route="mcp",le="0.025"} 0');
    expect(text).toContain('mcp_http_request_duration_seconds_bucket{route="mcp",le="0.05"} 1');
    expect(text).toContain('mcp_http_request_duration_seconds_bucket{route="mcp",le="1"} 2');
    expect(text).toContain('mcp_http_request_duration_seconds_bucket{route="mcp",le="+Inf"} 2');
    expect(text).toContain('mcp_http_request_duration_seconds_sum{route="mcp"} 0.64');
    expect(text).toContain('mcp_http_request_duration_seconds_count{route="mcp"} 2');
  });

  it("exposes the resolve-cache entry count as a gauge", () => {
    const m = new MetricsRegistry();
    m.setResolveCacheEntries(7);
    const text = m.render();
    expect(text).toContain("# TYPE mcp_resolve_cache_entries gauge");
    expect(text).toContain("mcp_resolve_cache_entries 7");
  });

  it("reset() clears every series back to empty", () => {
    const m = new MetricsRegistry();
    m.incHttpRequest("mcp", "2xx");
    m.incAuthResolution("resolved");
    m.incToolExecution("list_agents", "ok");
    m.incReadyzCheck("ok");
    m.observeRequestDuration("mcp", 0.04);
    m.setResolveCacheEntries(3);
    m.reset();
    const text = m.render();
    expect(text).not.toContain("mcp_http_requests_total{");
    expect(text).not.toContain("mcp_auth_resolutions_total{");
    expect(text).not.toContain("mcp_tool_executions_total{");
    expect(text).not.toContain("mcp_readyz_checks_total{");
    expect(text).not.toContain("mcp_http_request_duration_seconds_bucket{");
    expect(text).toContain("mcp_resolve_cache_entries 0");
  });
});

describe("GET /metrics", () => {
  let close: (() => Promise<void>) | undefined;

  afterEach(async () => {
    await close?.();
    close = undefined;
  });

  it("serves exposition text containing counters incremented by prior requests", async () => {
    const stub = makeStubClient();
    const started = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: () => stub,
      logger: () => {},
    });
    close = started.close;
    const origin = `http://127.0.0.1:${started.port}`;

    const health = await fetch(`${origin}/healthz`);
    expect(health.status).toBe(200);
    const unauth = await fetch(`${origin}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(unauth.status).toBe(401);
    await unauth.text();

    const res = await fetch(`${origin}/metrics`);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/plain");
    const text = await res.text();
    expect(text).toContain('mcp_http_requests_total{route="healthz",status_class="2xx"} 1');
    expect(text).toContain('mcp_http_requests_total{route="mcp",status_class="4xx"} 1');
    expect(text).toContain('mcp_http_request_duration_seconds_count{route="mcp"} 1');
    expect(text).toContain('mcp_http_request_duration_seconds_count{route="healthz"} 1');
    // The scrape itself is counted too (after this render, on finish).
    const second = await fetch(`${origin}/metrics`);
    expect(await second.text()).toContain('mcp_http_requests_total{route="metrics",status_class="2xx"} 1');
  });

  it("reports the resolve-cache gauge from live traffic", async () => {
    const stub = makeStubClient();
    const started = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: () => stub,
      logger: () => {},
    });
    close = started.close;
    const origin = `http://127.0.0.1:${started.port}`;

    const res = await fetch(`${origin}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: "Bearer gauge_token",
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(200);
    await res.text();

    const text = await (await fetch(`${origin}/metrics`)).text();
    expect(text).toContain("mcp_resolve_cache_entries 1");
    expect(text).toContain('mcp_auth_resolutions_total{result="resolved"} 1');
  });
});
