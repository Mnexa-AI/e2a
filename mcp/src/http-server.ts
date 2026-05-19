import crypto from "node:crypto";
import express, { type Express, type Request, type Response } from "express";
import cors from "cors";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { isInitializeRequest } from "@modelcontextprotocol/sdk/types.js";
import { E2AClient } from "@e2a/sdk/v1";
import { buildServer } from "./server.js";
import { Sessions } from "./session.js";

export interface HttpServerOptions {
  /** Base URL of the e2a backend (Bearer is forwarded as-is). */
  baseUrl: string;
  /** Hostnames the SDK will accept; rejects anything else with 421. */
  allowedHosts: string[];
  /** Optional shared sessions instance (defaults to a fresh one). */
  sessions?: Sessions;
  /** Session idle timeout. */
  sessionIdleMs?: number;
  /** Hard cap on concurrent sessions. */
  maxSessions?: number;
  /**
   * Test seam: override how the per-session E2AClient is built. The
   * default constructs `new E2AClient({ apiKey: bearer, baseUrl })`.
   */
  clientFactory?: (bearer: string) => E2AClient;
}

interface BuiltApp {
  app: Express;
  sessions: Sessions;
}

/**
 * Build the Express app that hosts the MCP Streamable HTTP transport.
 *
 * Per design (.claude/design/mcp-system.md §4.2): this server is pure
 * transport. It forwards the user's Bearer to the e2a backend
 * unchanged; the backend dispatches on token prefix (e2a_ vs ate2a_).
 * No token introspection, no caching, no service credential held here.
 */
export function buildApp(opts: HttpServerOptions): BuiltApp {
  const sessions =
    opts.sessions ??
    new Sessions({
      idleTimeoutMs: opts.sessionIdleMs ?? 5 * 60_000,
      maxSessions: opts.maxSessions ?? 500,
    });

  const app = express();
  app.use(express.json({ limit: "1mb" }));
  // CORS open for v0.2; revisit when we have a real allowlist of MCP hosts.
  app.use(cors({ origin: "*", exposedHeaders: ["Mcp-Session-Id"] }));

  app.get("/healthz", (_req, res) => {
    res.json({ ok: true });
  });

  // DNS rebinding protection. The SDK deprecated its in-transport allowlist
  // in favor of external middleware; we enforce it here. 421 Misdirected
  // Request is the spec-recommended status for "this server is not what
  // you asked for." Strip port before comparing so the same allowlist
  // entry works both for prod (`mcp.e2a.dev`) and tests on random ports
  // (`127.0.0.1:54321`).
  const allowedHosts = new Set(opts.allowedHosts.map((h) => h.toLowerCase()));
  app.use("/mcp", (req, res, next) => {
    const host = req.headers.host;
    if (!host) {
      res.status(421).end();
      return;
    }
    const bare = host.split(":")[0]!.toLowerCase();
    if (!allowedHosts.has(bare)) {
      res.status(421).end();
      return;
    }
    next();
  });

  // Spec-mandated discovery for hosts probing where the auth server lives.
  // Served unconditionally (even pre-OAuth) so clients don't get a confusing
  // 404 between v0.2 and v0.3. Validates Host against the allowlist to
  // avoid reflecting attacker-controlled hosts back in the `resource` URL.
  app.get("/.well-known/oauth-protected-resource", (req, res) => {
    const incoming = req.headers.host?.split(":")[0]?.toLowerCase();
    if (!incoming || !allowedHosts.has(incoming)) {
      res.status(421).end();
      return;
    }
    res.json({
      resource: `https://${req.headers.host}`,
      authorization_servers: [opts.baseUrl],
      scopes_supported: ["e2a"],
      bearer_methods_supported: ["header"],
    });
  });

  app.post("/mcp", async (req, res) => {
    await handleClientRequest(req, res, sessions, opts);
  });

  // Streamable HTTP uses GET for SSE notifications and DELETE for session termination.
  app.get("/mcp", async (req, res) => {
    await handleStreamingOrDelete(req, res, sessions);
  });
  app.delete("/mcp", async (req, res) => {
    await handleStreamingOrDelete(req, res, sessions);
  });

  return { app, sessions };
}

async function handleClientRequest(
  req: Request,
  res: Response,
  sessions: Sessions,
  opts: HttpServerOptions,
): Promise<void> {
  const bearer = extractBearer(req);
  if (!bearer) {
    res.setHeader(
      "WWW-Authenticate",
      `Bearer realm="e2a", resource_metadata="https://${req.headers.host}/.well-known/oauth-protected-resource"`,
    );
    res.status(401).json(jsonRpcError(req.body, -32001, "missing bearer token"));
    return;
  }

  const sessionId = req.headers["mcp-session-id"] as string | undefined;
  let entry = sessionId ? sessions.get(sessionId) : undefined;

  if (!entry && isInitializeRequest(req.body)) {
    const client = opts.clientFactory
      ? opts.clientFactory(bearer)
      : new E2AClient({ apiKey: bearer, baseUrl: opts.baseUrl });
    const server = buildServer({ client });
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: () => crypto.randomUUID(),
      onsessioninitialized: (id) => {
        sessions.put(id, { transport, server, lastSeen: Date.now() });
      },
    });
    // SDK chains any pre-existing onclose with its own internal cleanup
    // (protocol.js:220-223), so setting before connect() is safe — both
    // our handler and the SDK's run on transport close.
    transport.onclose = () => {
      const id = transport.sessionId;
      if (id) void sessions.delete(id);
    };
    try {
      await server.connect(transport);
      await transport.handleRequest(req, res, req.body);
    } catch (err) {
      // Initialize failed mid-flow. If onsessioninitialized fired, the
      // entry is in the map — delete() closes the transport. Otherwise
      // close directly to release SDK state. Either path is best-effort:
      // we're already on the error path and don't want to mask the
      // original failure.
      const sid = transport.sessionId;
      if (sid) {
        await sessions.delete(sid).catch(() => undefined);
      } else {
        await transport.close().catch(() => undefined);
      }
      throw err;
    }
    return;
  }
  if (!entry) {
    res.status(400).json(jsonRpcError(req.body, -32000, "no session — send initialize first"));
    return;
  }

  await entry.transport.handleRequest(req, res, req.body);
}

async function handleStreamingOrDelete(
  req: Request,
  res: Response,
  sessions: Sessions,
): Promise<void> {
  // Require Bearer on every /mcp request, including SSE GET and session
  // DELETE — knowing a session id should not be sufficient to read its
  // notification stream or terminate it.
  const bearer = extractBearer(req);
  if (!bearer) {
    res.setHeader(
      "WWW-Authenticate",
      `Bearer realm="e2a", resource_metadata="https://${req.headers.host}/.well-known/oauth-protected-resource"`,
    );
    res.status(401).end();
    return;
  }

  const sessionId = req.headers["mcp-session-id"] as string | undefined;
  if (!sessionId) {
    res.status(400).end();
    return;
  }
  const entry = sessions.get(sessionId);
  if (!entry) {
    res.status(404).end();
    return;
  }
  await entry.transport.handleRequest(req, res);
}

function extractBearer(req: Request): string | null {
  const auth = req.headers.authorization;
  if (!auth || typeof auth !== "string") return null;
  const m = /^Bearer\s+(.+)$/i.exec(auth);
  return m ? m[1]!.trim() : null;
}

function jsonRpcError(
  body: unknown,
  code: number,
  message: string,
): Record<string, unknown> {
  // Preserve the request id when we can identify it; otherwise null.
  const id =
    body && typeof body === "object" && "id" in body
      ? (body as { id: unknown }).id
      : null;
  return { jsonrpc: "2.0", id, error: { code, message } };
}

/**
 * Start the HTTP server on `port`. Returns a closer that stops the listener
 * and drains active sessions — wire it to SIGTERM in bin/http.ts.
 */
export async function startHttpServer(
  port: number,
  opts: HttpServerOptions,
): Promise<{ close: () => Promise<void>; port: number }> {
  const { app, sessions } = buildApp(opts);
  sessions.startGc();
  const server = app.listen(port);
  await new Promise<void>((resolve, reject) => {
    server.once("listening", resolve);
    server.once("error", reject);
  });
  const actualPort = (server.address() as { port: number }).port;
  return {
    port: actualPort,
    close: async () => {
      await new Promise<void>((resolve) => server.close(() => resolve()));
      await sessions.shutdown();
    },
  };
}
