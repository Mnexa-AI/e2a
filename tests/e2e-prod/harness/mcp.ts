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

export interface McpToolCall {
  name: string;
  arguments?: Record<string, unknown>;
}

export interface McpToolResult {
  content?: Array<{ type: string; text?: string }>;
  isError?: boolean;
}

export async function callTool(c: StdioMcpClient, name: string, args?: Record<string, unknown>): Promise<McpToolResult> {
  return c.call<McpToolResult>("tools/call", { name, arguments: args ?? {} });
}
