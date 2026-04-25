import { EventEmitter } from "node:events";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockExecFile = vi.fn();
const mockLoadConfig = vi.fn();
const mockSaveConfig = vi.fn();
const mockCreateServer = vi.fn();

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

    mockLoadConfig.mockReturnValue({
      api_key: "",
      api_url: "https://e2a.dev",
      agent_email: "",
    });
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
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
      agent_email: "bot@agents.e2a.dev",
    });
    expect(mockStdout).toHaveBeenCalledWith("Logged in. Config saved to ~/.e2a/config.json\n");
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
      agent_email: "",
    });
    expect(mockStderr).toHaveBeenCalledWith("No agents found yet. Run: e2a agents register <slug>\n");
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
