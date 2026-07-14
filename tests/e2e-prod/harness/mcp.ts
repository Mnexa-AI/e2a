import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";

interface JsonRpcRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: unknown;
}

interface JsonRpcResponse<T = unknown> {
  jsonrpc: "2.0";
  id: number;
  result?: T;
  error?: { code: number; message: string; data?: unknown };
}

export class StdioMcpClient {
  private proc!: ChildProcessWithoutNullStreams;
  private buf = "";
  private pending = new Map<number, (resp: JsonRpcResponse) => void>();
  private nextId = 1;
  private stderr = "";

  async start(command: string, args: string[], env: Record<string, string>): Promise<void> {
    this.proc = spawn(command, args, { env: { ...process.env, ...env }, stdio: ["pipe", "pipe", "pipe"] });
    this.proc.stdout.setEncoding("utf-8");
    this.proc.stderr.setEncoding("utf-8");
    this.proc.stdout.on("data", (chunk: string) => this.onStdout(chunk));
    this.proc.stderr.on("data", (chunk: string) => {
      this.stderr += chunk;
    });
    await this.call("initialize", {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "e2e-prod", version: "0.0.1" },
    });
    // MCP requires an initialized notification — fire-and-forget.
    this.notify("notifications/initialized", {});
  }

  private onStdout(chunk: string): void {
    this.buf += chunk;
    while (true) {
      const nl = this.buf.indexOf("\n");
      if (nl < 0) break;
      const line = this.buf.slice(0, nl).trim();
      this.buf = this.buf.slice(nl + 1);
      if (line.length === 0) continue;
      let msg: JsonRpcResponse;
      try {
        msg = JSON.parse(line);
      } catch {
        continue;
      }
      const cb = this.pending.get(msg.id);
      if (cb) {
        this.pending.delete(msg.id);
        cb(msg);
      }
    }
  }

  call<T = unknown>(method: string, params?: unknown, timeoutMs = 15_000): Promise<T> {
    const id = this.nextId++;
    const req: JsonRpcRequest = { jsonrpc: "2.0", id, method, params };
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`MCP call ${method} timed out after ${timeoutMs}ms. stderr: ${this.stderr.slice(0, 500)}`));
      }, timeoutMs);
      this.pending.set(id, (resp) => {
        clearTimeout(timer);
        if (resp.error) {
          reject(new Error(`MCP ${method} error ${resp.error.code}: ${resp.error.message}`));
        } else {
          resolve(resp.result as T);
        }
      });
      this.proc.stdin.write(JSON.stringify(req) + "\n");
    });
  }

  notify(method: string, params: unknown): void {
    const msg = { jsonrpc: "2.0", method, params };
    this.proc.stdin.write(JSON.stringify(msg) + "\n");
  }

  async stop(): Promise<void> {
    try {
      this.proc.stdin.end();
    } catch {}
    return new Promise((resolve) => {
      const t = setTimeout(() => {
        try {
          this.proc.kill("SIGKILL");
        } catch {}
        resolve();
      }, 2_000);
      this.proc.once("exit", () => {
        clearTimeout(t);
        resolve();
      });
    });
  }

  getStderr(): string {
    return this.stderr;
  }
}

/**
 * The single MCP surface the suites depend on: one JSON-RPC round-trip.
 * Both the stdio child-process client and the streamable-HTTP client
 * implement it, so a suite can swap transports without touching test bodies.
 */
export interface McpRpcClient {
  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number): Promise<T>;
}

/**
 * MCP client that talks to a DEPLOYED streamable-HTTP `/mcp` server over
 * plain `fetch` — no child process, no `@modelcontextprotocol/sdk` (this
 * package is intentionally zero-dependency). This exercises the same image
 * that ships to prod/staging, not a locally-built stdio binary.
 *
 * The server is a **stateless** transport (`sessionIdGenerator: undefined`
 * in mcp/src/http-server.ts): there is no `initialize` handshake and no
 * `Mcp-Session-Id` — a bare `tools/list` / `tools/call` dispatches on its
 * own. The user's Bearer is forwarded to the e2a backend as-is. The SDK
 * answers a POST as an SSE stream (`text/event-stream`) unless JSON
 * responses are enabled (they aren't), so we accept both framings and parse
 * accordingly — mirroring the Go prober's `mcpCall` (internal/selftest).
 */
export class HttpMcpClient implements McpRpcClient {
  private nextId = 1;
  private readonly url: string;
  private readonly apiKey: string;

  // Plain field assignment (not constructor parameter properties) — the suites
  // run under `node --test` strip-only mode, which rejects param properties.
  constructor(url: string, apiKey: string) {
    this.url = url;
    this.apiKey = apiKey;
  }

  async call<T = unknown>(method: string, params?: unknown, timeoutMs = 15_000): Promise<T> {
    const id = this.nextId++;
    const body: JsonRpcRequest = { jsonrpc: "2.0", id, method, params };
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), timeoutMs);
    let resp: Response;
    try {
      resp = await fetch(this.url, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.apiKey}`,
          "Content-Type": "application/json",
          // Streamable-HTTP requires the client to accept both framings.
          Accept: "application/json, text/event-stream",
        },
        body: JSON.stringify(body),
        signal: ctrl.signal,
      });
    } catch (err) {
      const reason = (err as Error)?.name === "AbortError" ? `timed out after ${timeoutMs}ms` : String(err);
      throw new Error(`MCP call ${method} failed: ${reason} (POST ${this.url})`);
    } finally {
      clearTimeout(timer);
    }
    const raw = await resp.text();
    if (!resp.ok) {
      throw new Error(`MCP ${method}: HTTP ${resp.status}. body: ${raw.slice(0, 500)}`);
    }
    const env = parseJsonRpcEnvelope<T>(raw, resp.headers.get("content-type") ?? "");
    if (!env) {
      throw new Error(`MCP ${method}: no JSON-RPC message in response. body: ${raw.slice(0, 500)}`);
    }
    if (env.error) {
      throw new Error(`MCP ${method} error ${env.error.code}: ${env.error.message}`);
    }
    return env.result as T;
  }

  // No process/socket to tear down — present so suites can call it uniformly.
  async stop(): Promise<void> {}
}

/**
 * Decode a JSON-RPC message from an MCP streamable-HTTP response, accepting
 * either a bare JSON body or an SSE stream. For SSE, walk each event's
 * `data:` lines (successive `data:` lines within one event join with `\n`)
 * and return the first that decodes to a message carrying a `jsonrpc` key
 * (ignoring pings / other event types). Mirrors the prober's Go parser.
 */
function parseJsonRpcEnvelope<T>(raw: string, contentType: string): JsonRpcResponse<T> | null {
  if (contentType.includes("text/event-stream")) {
    const body = raw.replace(/\r\n/g, "\n");
    for (const event of body.split("\n\n")) {
      const dataLines: string[] = [];
      for (const line of event.split("\n")) {
        if (line.startsWith("data:")) dataLines.push(line.slice(5).replace(/^ /, ""));
      }
      if (dataLines.length === 0) continue;
      let msg: JsonRpcResponse<T>;
      try {
        msg = JSON.parse(dataLines.join("\n"));
      } catch {
        continue;
      }
      if (msg && (msg as { jsonrpc?: unknown }).jsonrpc) return msg;
    }
    return null;
  }
  try {
    return JSON.parse(raw) as JsonRpcResponse<T>;
  } catch {
    return null;
  }
}

export interface McpToolCall {
  name: string;
  arguments?: Record<string, unknown>;
}

export interface McpToolResult {
  content?: Array<{ type: string; text?: string }>;
  isError?: boolean;
}

export async function callTool(c: McpRpcClient, name: string, args?: Record<string, unknown>): Promise<McpToolResult> {
  return c.call<McpToolResult>("tools/call", { name, arguments: args ?? {} });
}
