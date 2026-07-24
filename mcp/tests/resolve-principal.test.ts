import { afterEach, describe, expect, it, vi } from "vitest";
import { resolvePrincipal } from "../src/http-server.js";
import type { McpClient } from "../src/client.js";

// Unit-level coverage for the bounded whoami probe (MCP_RESOLVE_TIMEOUT_MS).
// resolvePrincipal is the exact seam authenticateClient uses, so the timeout
// behavior proven here is what every POST /mcp gets.
describe("resolvePrincipal bounded whoami probe", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function factoryReturning(client: Partial<McpClient>) {
    return vi.fn(() => client as McpClient);
  }

  it("a hung whoami falls back to least-privilege after resolveTimeoutMs (fake clock, no real wait)", async () => {
    vi.useFakeTimers();
    const whoami = vi.fn(() => new Promise<never>(() => {})); // never settles
    const factory = factoryReturning({ whoami: whoami as McpClient["whoami"] });

    const promise = resolvePrincipal(
      {
        baseUrl: "http://e2a.local",
        allowedHosts: [],
        resolveTimeoutMs: 5000,
        clientFactory: factory,
      },
      "tok_slow",
    );
    const assertion = expect(promise).resolves.toEqual({
      value: { scope: "agent" },
      cacheable: false,
    });
    // Advance past the probe timeout; the race must settle on its own.
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
    expect(whoami).toHaveBeenCalledOnce();
  });

  it("a hung whoami does NOT resolve early (timeout is the trigger, not a fixed delay)", async () => {
    vi.useFakeTimers();
    const whoami = vi.fn(() => new Promise<never>(() => {}));
    const factory = factoryReturning({ whoami: whoami as McpClient["whoami"] });

    let settled = false;
    const promise = resolvePrincipal(
      {
        baseUrl: "http://e2a.local",
        allowedHosts: [],
        resolveTimeoutMs: 5000,
        clientFactory: factory,
      },
      "tok_slow",
    ).then((r) => {
      settled = true;
      return r;
    });
    await vi.advanceTimersByTimeAsync(4999);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    await promise;
    expect(settled).toBe(true);
  });

  it("a fast whoami resolves normally within the timeout", async () => {
    const whoami = vi.fn(async () => ({
      user: "owner@example.com",
      scope: "agent",
      agentEmail: "bot@example.com",
    }));
    const factory = factoryReturning({ whoami: whoami as McpClient["whoami"] });
    const resolved = await resolvePrincipal(
      {
        baseUrl: "http://e2a.local",
        allowedHosts: [],
        resolveTimeoutMs: 5000,
        clientFactory: factory,
      },
      "tok_fast",
    );
    expect(resolved).toEqual({
      value: { scope: "agent", agentEmail: "bot@example.com" },
      cacheable: true,
    });
  });

  it("a 401 within the timeout still maps to the invalid-bearer challenge", async () => {
    const err = new Error("HTTP 401") as Error & { statusCode: number };
    err.statusCode = 401;
    const whoami = vi.fn(async () => {
      throw err;
    });
    const factory = factoryReturning({ whoami: whoami as McpClient["whoami"] });
    await expect(
      resolvePrincipal(
        {
          baseUrl: "http://e2a.local",
          allowedHosts: [],
          resolveTimeoutMs: 5000,
          clientFactory: factory,
        },
        "tok_expired",
      ),
    ).rejects.toThrowError(/invalid bearer/);
  });

  it("a non-auth failure within the timeout keeps the existing fail-closed fallback", async () => {
    const whoami = vi.fn(async () => {
      throw new Error("upstream 500");
    });
    const factory = factoryReturning({ whoami: whoami as McpClient["whoami"] });
    const resolved = await resolvePrincipal(
      {
        baseUrl: "http://e2a.local",
        allowedHosts: [],
        resolveTimeoutMs: 5000,
        clientFactory: factory,
      },
      "tok_blip",
    );
    expect(resolved).toEqual({ value: { scope: "agent" }, cacheable: false });
  });
});
