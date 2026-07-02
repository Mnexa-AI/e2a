import { EventEmitter } from "node:events";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockExecFile = vi.fn();
const mockLoadConfig = vi.fn();
const mockSaveConfig = vi.fn();
const mockCreateServer = vi.fn();
const mockFetch = vi.fn();
const originalFetch = globalThis.fetch;

let currentServerHandler: ((req: any, res: any) => void | Promise<void>) | null = null;

vi.mock("node:child_process", () => ({
  execFile: mockExecFile,
}));

vi.mock("node:http", () => ({
  createServer: mockCreateServer.mockImplementation((handler: (req: any, res: any) => void | Promise<void>) => {
    currentServerHandler = handler;

    const server = {
      listening: false,
      on: vi.fn(() => server),
      listen: vi.fn((_port: number, _host: string, cb?: () => void) => {
        server.listening = true;
        cb?.();
        return server;
      }),
      address: vi.fn(() => ({ port: 43123, family: "IPv4", address: "127.0.0.1" })),
      close: vi.fn((cb?: () => void) => {
        server.listening = false;
        cb?.();
        return server;
      }),
    };

    return server;
  }),
}));

vi.mock("../config.js", () => ({
  loadConfig: mockLoadConfig,
  saveConfig: mockSaveConfig,
}));

// login probes GET /v1/info with a raw fetch (pre-auth, before a key exists),
// so we stub the global fetch. infoResponse() builds the success shape; tests
// override per scenario (unreachable -> reject; older deployment -> ok:false).
function infoResponse(sharedDomain: string) {
  return {
    ok: true,
    json: async () => ({ shared_domain: sharedDomain, slug_registration_enabled: true }),
  };
}

async function simulateBrowserCallback(payload: Record<string, string>) {
  if (!currentServerHandler) {
    throw new Error("missing loopback server handler");
  }

  const req = new EventEmitter() as EventEmitter & {
    method: string;
    setEncoding: (encoding: string) => void;
  };
  req.method = "POST";
  req.setEncoding = vi.fn();

  const res = {
    statusCode: 200,
    headers: {} as Record<string, string>,
    setHeader: vi.fn((name: string, value: string) => {
      res.headers[name] = value;
    }),
    end: vi.fn(),
  };

  const body = new URLSearchParams(payload).toString();
  const handlerPromise = Promise.resolve(currentServerHandler(req, res));

  queueMicrotask(() => {
    req.emit("data", body);
    req.emit("end");
  });

  await handlerPromise;
  expect(res.end).toHaveBeenCalled();
}

describe("login", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);

    currentServerHandler = null;
    mockLoadConfig.mockReset();
    mockSaveConfig.mockReset();
    mockExecFile.mockReset();
    mockCreateServer.mockClear();
    mockFetch.mockReset();
    // Default: deployment exposes the hosted shared domain. Override per-test
    // for self-host / older-deployment / unreachable scenarios.
    mockFetch.mockResolvedValue(infoResponse("agents.e2a.dev"));
    globalThis.fetch = mockFetch as unknown as typeof fetch;

    mockLoadConfig.mockReturnValue({
      api_key: "",
      api_url: "https://e2a.dev",
      agent_email: "",
      shared_domain: "agents.e2a.dev",
    });
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    globalThis.fetch = originalFetch;
    vi.clearAllMocks();
  });

  it("saves the api key and active agent from the browser callback", async () => {
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_browser_key",
        agent_email: "bot@agents.e2a.dev",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await login();

    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_browser_key",
      key_scope: "account",
      agent_email: "bot@agents.e2a.dev",
      shared_domain: "agents.e2a.dev",
    });
    expect(mockStdout).toHaveBeenCalledWith("Logged in to e2a.dev.\n");
    expect(mockStdout).toHaveBeenCalledWith("Config saved to ~/.e2a/config.json\n");
    expect(mockStdout).toHaveBeenCalledWith("Active agent: bot@agents.e2a.dev\n");
  });

  it("clears the active agent when the browser login returns no agents", async () => {
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_browser_key",
        agent_email: "",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await login();

    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_browser_key",
      key_scope: "account",
      agent_email: "",
      shared_domain: "agents.e2a.dev",
    });
    expect(mockStderr).toHaveBeenCalledWith("No agents found yet. Run: e2a agents create <name>@<shared-domain> — or visit https://e2a.dev/get-started\n");
  });

  it("unrefs the browser child process so Node can exit", async () => {
    const mockUnref = vi.fn();
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_browser_key",
        agent_email: "bot@agents.e2a.dev",
      });

      cb?.(null);
      return { unref: mockUnref };
    });

    const { login } = await import("../commands/login.js");
    await login();

    expect(mockUnref).toHaveBeenCalled();
  });

  it("fast-fails before opening the browser when the server is unreachable", async () => {
    // Simulate a network failure (connection refused, DNS, etc.) — info()
    // throws E2AConnectionError, so login() should abort before kicking off
    // the browser flow.
    mockFetch.mockRejectedValueOnce(new TypeError("fetch failed"));

    const { login } = await import("../commands/login.js");
    await expect(login()).rejects.toThrow(/could not reach https:\/\/e2a\.dev/);

    expect(mockExecFile).not.toHaveBeenCalled();
    expect(mockSaveConfig).not.toHaveBeenCalled();
  });

  it("continues login when info() responds with an HTTP error (older deployment)", async () => {
    // Server is reachable but doesn't expose the info endpoint. Login
    // should proceed and just skip the shared_domain field in saveConfig.
    mockFetch.mockResolvedValueOnce({ ok: false, status: 404, json: async () => ({}) });
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_browser_key",
        agent_email: "bot@example.com",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await login();

    // shared_domain absent — older deployment couldn't be discovered
    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_browser_key",
      key_scope: "account",
      agent_email: "bot@example.com",
    });
  });

  it("prints the login URL as a fallback for headless environments", async () => {
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_browser_key",
        agent_email: "bot@agents.e2a.dev",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await login();

    // Confirm the manual-fallback hint is always printed, not just when
    // openBrowser errors. Headless boxes/SSH/containers depend on this.
    const stderrCalls = mockStderr.mock.calls
      .map((c: unknown[]) => String(c[0]))
      .join("");
    expect(stderrCalls).toMatch(/If it doesn't open, visit:/);
    expect(stderrCalls).toMatch(/\/api\/auth\/login/);
  });

  it("confirms the discovered shared domain in the success output", async () => {
    mockFetch.mockResolvedValueOnce(infoResponse("agents.acme.test"));
    mockLoadConfig.mockReturnValue({
      api_key: "",
      api_url: "https://e2a.acme.test",
      agent_email: "",
      shared_domain: "",
    });
    mockExecFile.mockImplementation((_cmd: string, args: string[], cb?: (err: Error | null) => void) => {
      const loginUrl = new URL(args[args.length - 1]);
      const cliState = loginUrl.searchParams.get("cli_state");

      void simulateBrowserCallback({
        cli_state: cliState!,
        api_key: "e2a_self",
        agent_email: "bot@agents.acme.test",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await login();

    expect(mockSaveConfig).toHaveBeenCalledWith({
      api_key: "e2a_self",
      key_scope: "account",
      agent_email: "bot@agents.acme.test",
      shared_domain: "agents.acme.test",
    });
    expect(mockStdout).toHaveBeenCalledWith("Logged in to e2a.acme.test.\n");
    expect(mockStdout).toHaveBeenCalledWith("  Shared domain: agents.acme.test\n");
  });

  it("fails when the browser callback state does not match", async () => {
    mockExecFile.mockImplementation((_cmd: string, _args: string[], cb?: (err: Error | null) => void) => {
      void simulateBrowserCallback({
        cli_state: "wrong-state",
        api_key: "e2a_browser_key",
        agent_email: "bot@agents.e2a.dev",
      });

      cb?.(null);
      return { unref: vi.fn() };
    });

    const { login } = await import("../commands/login.js");
    await expect(login()).rejects.toThrow("browser login state mismatch");
    expect(mockSaveConfig).not.toHaveBeenCalled();
  });
});
