import crypto from "node:crypto";
import express, { type Express, type Request, type Response } from "express";
import cors from "cors";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { isInitializeRequest } from "@modelcontextprotocol/sdk/types.js";
import { E2AClient } from "@e2a/sdk/v1";
import { buildServer } from "./server.js";
import { McpClient } from "./client.js";
import { Sessions, fingerprintBearer } from "./session.js";

export interface HttpServerOptions {
  /** Base URL of the e2a backend (Bearer is forwarded as-is). */
  baseUrl: string;
  /** Hostnames the SDK will accept; rejects anything else with 421. */
  allowedHosts: string[];
  /**
   * Externally reachable URL of this MCP server. When set, used verbatim
   * for the RFC 9728 protected-resource metadata `resource` field and
   * the `WWW-Authenticate: resource_metadata=...` value on 401s. When
   * unset (production default behind Caddy), we synthesize
   * `https://{Host}` from the inbound request. The local-dev runbook
   * sets this to `http://localhost:8765` so Claude Code's OAuth probe
   * can resolve the metadata over loopback http.
   */
  publicUrl?: string;
  /**
   * Externally reachable URL of the authorization server. Defaults to
   * baseUrl when unset — fine in prod where baseUrl is e2a.dev. In a
   * Docker compose setup the bearer-forwarding side (baseUrl) needs
   * the container-internal hostname (http://e2a:8080) while the OAuth
   * client needs the host-reachable URL (http://localhost:8080). Set
   * this to override what's advertised in protected-resource metadata
   * without changing where the SDK forwards bearer tokens.
   */
  authorizationServerUrl?: string;
  /** Optional shared sessions instance (defaults to a fresh one). */
  sessions?: Sessions;
  /** Session idle timeout. */
  sessionIdleMs?: number;
  /** Hard cap on concurrent sessions. */
  maxSessions?: number;
  /**
   * Test seam: override how the per-session E2AClient is built. The
   * default constructs `new E2AClient({ apiKey: bearer, baseUrl })`.
   *
   * The optional second arg carries the agent_email resolved by the
   * single-agent prefetch (see {@link buildSessionClient}). Tests that
   * want to exercise the prefetch path can pass it through to their
   * stub; tests that only care about the bearer can ignore it.
   */
  clientFactory?: (bearer: string, opts?: { agentEmail?: string }) => McpClient;
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
  // entry works both for prod (`api.e2a.dev`) and tests on random ports
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
  // Compute the public-facing URL of this MCP server. Three cases:
  //   1. publicUrl set explicitly: trust it verbatim (local-dev http,
  //      or any deployment behind a fronting proxy that knows its own
  //      external URL better than we do).
  //   2. unset + Host header present + Host in allowlist: synthesize
  //      `https://{Host}`. This is the prod-default Caddy-fronted path.
  //   3. unset + Host missing/disallowed: caller wrapped function
  //      rejects with 421 before reaching here.
  const resolveResourceUrl = (req: Request): string | null => {
    if (opts.publicUrl) {
      return opts.publicUrl.replace(/\/+$/, "");
    }
    const host = req.headers.host;
    if (!host) return null;
    const bare = host.split(":")[0]!.toLowerCase();
    if (!allowedHosts.has(bare)) return null;
    return `https://${host}`;
  };

  app.get("/.well-known/oauth-protected-resource", (req, res) => {
    const resource = resolveResourceUrl(req);
    if (!resource) {
      res.status(421).end();
      return;
    }
    res.json({
      resource,
      authorization_servers: [opts.authorizationServerUrl ?? opts.baseUrl],
      // Scope vocabulary tracks the AS (Slice 5b): the lone "mcp" scope is
      // retired. MCP clients connect as public DCR clients, which are capped at
      // scope="agent" by the authorization server, so advertise that here so the
      // protected-resource and AS metadata agree.
      scopes_supported: ["agent"],
      bearer_methods_supported: ["header"],
    });
  });

  app.post("/mcp", async (req, res) => {
    await handleClientRequest(req, res, sessions, opts);
  });

  // Streamable HTTP uses GET for SSE notifications and DELETE for session termination.
  app.get("/mcp", async (req, res) => {
    await handleStreamingOrDelete(req, res, sessions, opts);
  });
  app.delete("/mcp", async (req, res) => {
    await handleStreamingOrDelete(req, res, sessions, opts);
  });

  return { app, sessions };
}

// resourceMetadataURL returns the value the `WWW-Authenticate` header
// should advertise. Honors publicUrl when set (local-dev http), falls
// back to https+Host otherwise (prod behind Caddy).
function resourceMetadataURL(req: Request, opts: HttpServerOptions): string {
  const base = opts.publicUrl
    ? opts.publicUrl.replace(/\/+$/, "")
    : `https://${req.headers.host}`;
  return `${base}/.well-known/oauth-protected-resource`;
}

function bearerChallenge(req: Request, opts: HttpServerOptions): string {
  return `Bearer realm="e2a", resource_metadata="${resourceMetadataURL(req, opts)}"`;
}

class InvalidBearerError extends Error {
  constructor() {
    super("invalid bearer token");
    this.name = "InvalidBearerError";
  }
}

async function handleClientRequest(
  req: Request,
  res: Response,
  sessions: Sessions,
  opts: HttpServerOptions,
): Promise<void> {
  const bearer = extractBearer(req);
  if (!bearer) {
    res.setHeader("WWW-Authenticate", bearerChallenge(req, opts));
    res.status(401).json(jsonRpcError(req.body, -32001, "missing bearer token"));
    return;
  }

  const sessionId = req.headers["mcp-session-id"] as string | undefined;
  let entry = sessionId ? sessions.get(sessionId) : undefined;

  // Session-bearer binding. The per-session E2AClient has the original
  // bearer baked in and forwards it verbatim to the e2a backend on
  // every tool call — so dispatching to a session by id alone, with
  // no bearer check, would let anyone who learned `Mcp-Session-Id`
  // act as the session owner (Access-Control-Expose-Headers exposes
  // the id, and the spec doesn't treat it as a secret). We refuse to
  // dispatch when the bearer's fingerprint doesn't match the one
  // captured at session-create.
  if (entry && entry.bearerFingerprint !== fingerprintBearer(bearer)) {
    res.setHeader(
      "WWW-Authenticate",
      `Bearer realm="e2a", error="invalid_token", error_description="bearer does not match session"`,
    );
    res.status(401).json(jsonRpcError(req.body, -32001, "session bearer mismatch"));
    return;
  }

  if (!entry && isInitializeRequest(req.body)) {
    let client: McpClient;
    try {
      client = await buildSessionClient(opts, bearer);
    } catch (err) {
      if (err instanceof InvalidBearerError) {
        res.setHeader(
          "WWW-Authenticate",
          `${bearerChallenge(req, opts)}, error="invalid_token"`,
        );
        res.status(401).json(jsonRpcError(req.body, -32001, "invalid bearer token"));
        return;
      }
      throw err;
    }
    const server = buildServer({ client });
    const bearerFp = fingerprintBearer(bearer);
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: () => crypto.randomUUID(),
      onsessioninitialized: (id) => {
        sessions.put(id, {
          transport,
          server,
          lastSeen: Date.now(),
          bearerFingerprint: bearerFp,
        });
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
    res.status(400).json(jsonRpcError(req.body, -32000, "no session -- send initialize first"));
    return;
  }

  await entry.transport.handleRequest(req, res, req.body);
}

async function handleStreamingOrDelete(
  req: Request,
  res: Response,
  sessions: Sessions,
  opts: HttpServerOptions,
): Promise<void> {
  // Require Bearer on every /mcp request, including SSE GET and session
  // DELETE — knowing a session id should not be sufficient to read its
  // notification stream or terminate it.
  const bearer = extractBearer(req);
  if (!bearer) {
    res.setHeader("WWW-Authenticate", bearerChallenge(req, opts));
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
  // Session-bearer binding (same rationale as handleClientRequest):
  // SSE notifications and session DELETE both carry the privilege of
  // the original initialize; the inline comment that used to be here
  // ("knowing a session id should not be sufficient...") is correct
  // and now enforced. A bearer-fingerprint mismatch rejects with
  // 401 + the standard WWW-Authenticate challenge.
  if (entry.bearerFingerprint !== fingerprintBearer(bearer)) {
    res.setHeader(
      "WWW-Authenticate",
      `Bearer realm="e2a", error="invalid_token", error_description="bearer does not match session"`,
    );
    res.status(401).end();
    return;
  }
  await entry.transport.handleRequest(req, res);
}

/**
 * Construct the per-session E2AClient, opportunistically resolving a
 * default agent_email when the user has exactly one agent.
 *
 * Why this lives here and not in the SDK: the SDK is a thin contract
 * shared by CLI, browser, server, and Python users — auto-resolving an
 * agent on construction would be too magical for those callers. The
 * MCP transport, on the other hand, has a clear notion of "session
 * init": one prefetch per session is cheap and lets every subsequent
 * tool call dispatch without forcing the LLM (or the user) to pass
 * email explicitly. The OAuth grant already binds the user to
 * one agent at consent, so for the dominant single-agent case the
 * default is unambiguous.
 *
 * Resolution order (highest precedence first):
 *   1. listAgents() yields exactly one agent — pin it as the default.
 *   2. Otherwise leave the default empty. Tools that take an optional
 *      `email` arg require it explicitly (resolveAddress errors with a
 *      hint pointing at list_agents).
 *
 * (There is no env-var default: E2A_AGENT_EMAIL was removed. An
 * agent-scoped credential resolves its agent server-side.)
 *
 * listAgents failures are swallowed: a transient backend hiccup
 * shouldn't break MCP initialize. Worst case, the user sees the same
 * "email is required" error they'd see today.
 */
export async function buildSessionClient(
  opts: HttpServerOptions,
  bearer: string,
): Promise<McpClient> {
  const make = (agentEmail?: string): McpClient => {
    if (opts.clientFactory) {
      // Preserve the no-arg-factory shape for callers (and tests) that
      // don't care about the resolved email. Pass the email through
      // only when we actually have one.
      return agentEmail
        ? opts.clientFactory(bearer, { agentEmail })
        : opts.clientFactory(bearer);
    }
    return new McpClient(
      new E2AClient({ apiKey: bearer, baseUrl: opts.baseUrl }),
      agentEmail ?? "",
    );
  };

  const client = make();
  let resolved: string | undefined;
  try {
    const agents = await client.listAgents();
    // Env-var path already populated agentEmail — operator opted in
    // explicitly, don't second-guess it with auto-resolution.
    if (!client.agentEmail && Array.isArray(agents) && agents.length === 1) {
      resolved = agents[0]?.email;
    }
  } catch (err) {
    if (isUnauthorizedError(err)) {
      throw new InvalidBearerError();
    }
    // Non-auth failures remain best-effort. A transient backend hiccup
    // shouldn't break MCP initialize; worst case, the user sees the
    // same "agentEmail is required" error they'd see today.
  }
  return resolved ? make(resolved) : client;
}

function isUnauthorizedError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const maybe = err as {
    status?: unknown;
    statusCode?: unknown;
    response?: { status?: unknown };
  };
  return (
    maybe.status === 401
    || maybe.statusCode === 401
    || maybe.response?.status === 401
  );
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
