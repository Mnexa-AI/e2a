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

// McpClient is the transport-agnostic surface the suites use: a JSON-RPC `call`
// plus lifecycle. Both the stdio client (local dev) and the HTTP client (the
// deployed streamable-HTTP server) implement it, so suites are transport-blind.
export interface McpClient {
  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number): Promise<T>;
  stop(): Promise<void>;
  getStderr(): string;
}

export class StdioMcpClient implements McpClient {
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

// HttpMcpClient drives the DEPLOYED streamable-HTTP MCP server (the artifact
// that actually ships — mcp is HTTP-only now; the stdio entry is retired). The
// server is stateless (no session id, no initialize handshake) and authenticates
// each POST with the caller's Bearer API key, so a "call" is one self-contained
// POST /mcp. The response is SSE by default (or JSON if the server enables it).
export class HttpMcpClient implements McpClient {
  private nextId = 1;
  private lastErr = "";
  private readonly url: string;
  private readonly apiKey: string;
  // Assign in the body (not constructor parameter properties): Node's
  // --experimental-strip-types only erases annotations, it can't emit the
  // implicit field assignments that `private` parameters require.
  constructor(url: string, apiKey: string) {
    this.url = url;
    this.apiKey = apiKey;
  }

  // Stateless — nothing to spawn or hand-shake.
  async start(): Promise<void> {}

  async call<T = unknown>(method: string, params?: unknown, timeoutMs = 15_000): Promise<T> {
    const body: JsonRpcRequest = { jsonrpc: "2.0", id: this.nextId++, method, params };
    let res: Response;
    try {
      res = await fetch(this.url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json, text/event-stream",
          Authorization: `Bearer ${this.apiKey}`,
        },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(timeoutMs),
      });
    } catch (e) {
      this.lastErr = `MCP ${method} transport error: ${(e as Error).message}`;
      throw new Error(this.lastErr);
    }
    const raw = await res.text();
    if (res.status !== 200) {
      this.lastErr = `MCP ${method}: HTTP ${res.status}: ${raw.slice(0, 300)}`;
      throw new Error(this.lastErr);
    }
    const env = parseJsonRpcEnvelope<T>(raw, res.headers.get("content-type") ?? "");
    if (env.error) {
      throw new Error(`MCP ${method} error ${env.error.code}: ${env.error.message}`);
    }
    return env.result as T;
  }

  async stop(): Promise<void> {}

  getStderr(): string {
    return this.lastErr;
  }
}

// parseJsonRpcEnvelope decodes a JSON-RPC message from a streamable-HTTP
// response: either a bare JSON body, or an SSE stream whose `data:` lines carry
// the message. Mirrors the prober's Go parser.
function parseJsonRpcEnvelope<T>(raw: string, contentType: string): JsonRpcResponse<T> {
  if (contentType.includes("text/event-stream")) {
    for (const event of raw.replace(/\r\n/g, "\n").split("\n\n")) {
      const data = event
        .split("\n")
        .filter((l) => l.startsWith("data:"))
        .map((l) => l.slice(5).replace(/^ /, ""))
        .join("\n");
      if (!data) continue;
      try {
        const env = JSON.parse(data) as JsonRpcResponse<T>;
        if (env.jsonrpc) return env;
      } catch {
        // not the JSON-RPC frame (ping/comment); keep scanning.
      }
    }
    throw new Error("no JSON-RPC message in SSE stream");
  }
  return JSON.parse(raw) as JsonRpcResponse<T>;
}

export interface McpToolCall {
  name: string;
  arguments?: Record<string, unknown>;
}

export interface McpToolResult {
  content?: Array<{ type: string; text?: string }>;
  isError?: boolean;
}

export async function callTool(c: McpClient, name: string, args?: Record<string, unknown>): Promise<McpToolResult> {
  return c.call<McpToolResult>("tools/call", { name, arguments: args ?? {} });
}
