import express, { type Express, type Request, type Response } from "express";
import cors from "cors";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { E2AClient } from "@e2a/sdk/v1";
import { buildServer } from "./server.js";
import { McpClient } from "./client.js";
import type { Scope } from "./tools/tiers.js";
import { ResolveCache, type ResolvedPrincipal } from "./resolve.js";

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
  /** Optional shared resolution cache (defaults to a fresh one). */
  resolveCache?: ResolveCache;
  /** How long a resolved bearer→principal entry stays cached (ms). */
  resolveCacheTtlMs?: number;
  /** Hard cap on cached principals. */
  resolveCacheMaxEntries?: number;
  /**
   * Test seam: override how the per-request E2AClient is built. The
   * default constructs `new E2AClient({ apiKey: bearer, baseUrl })`.
   *
   * The optional second arg carries the agent_email + scope resolved by the
   * per-bearer `whoami` prefetch (see {@link resolvePrincipal}). Tests that
   * want to exercise the prefetch path can pass it through to their stub;
   * tests that only care about the bearer can ignore it.
   */
  clientFactory?: (bearer: string, opts?: { agentEmail?: string; scope?: Scope }) => McpClient;
}

interface BuiltApp {
  app: Express;
  cache: ResolveCache;
}

/**
 * Build the Express app that hosts the MCP Streamable HTTP transport.
 *
 * Per design (.claude/design/mcp-system.md §4.2): this server is pure,
 * **stateless** transport. It forwards the user's Bearer to the e2a backend
 * unchanged (the backend dispatches on token prefix e2a_ vs ate2a_) and holds
 * no per-connection session state — every request builds a fresh server +
 * transport and dispatches independently. The only thing kept between requests
 * is a short-lived bearer→principal cache so a burst of tool calls doesn't pay
 * a `whoami` round-trip each. There is no session idle GC, so an idle client is
 * never disconnected; only the bearer's own expiry (handled by the client's
 * OAuth refresh) ever ends a connection.
 */
export function buildApp(opts: HttpServerOptions): BuiltApp {
  const cache =
    opts.resolveCache ??
    new ResolveCache({
      ttlMs: opts.resolveCacheTtlMs ?? 60_000,
      maxEntries: opts.resolveCacheMaxEntries ?? 500,
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
      // retired. MCP clients connect as public DCR clients; the consent screen
      // grants "agent" (single inbox) or "account" (workspace admin) when the
      // user opts in on an account-eligible client (loopback or https — see the
      // backend's accountEligibleRedirect). Both are advertised so a
      // spec-compliant client requests the full menu and the protected-resource
      // and AS metadata agree.
      scopes_supported: ["agent", "account"],
      bearer_methods_supported: ["header"],
    });
  });

  app.post("/mcp", async (req, res) => {
    await handleClientRequest(req, res, cache, opts);
  });

  // Stateless transport: there is no standalone SSE stream and no session to
  // terminate, so the GET (SSE notifications) and DELETE (session teardown)
  // verbs the Streamable-HTTP spec defines for stateful servers don't apply.
  // Answer both with 405 + a JSON-RPC error, matching the SDK's stateless
  // reference server.
  app.get("/mcp", methodNotAllowed);
  app.delete("/mcp", methodNotAllowed);

  return { app, cache };
}

function methodNotAllowed(_req: Request, res: Response): void {
  res
    .status(405)
    .json(jsonRpcError(null, -32000, "Method not allowed: the e2a MCP server is stateless"));
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
  cache: ResolveCache,
  opts: HttpServerOptions,
): Promise<void> {
  const bearer = extractBearer(req);
  if (!bearer) {
    res.setHeader("WWW-Authenticate", bearerChallenge(req, opts));
    res.status(401).json(jsonRpcError(req.body, -32001, "missing bearer token"));
    return;
  }

  // Resolve the credential's scope + bound agent. A cache hit skips the
  // backend round-trip; a miss probes `whoami` once and caches the result.
  // A revoked/expired bearer surfaces as 401 here (InvalidBearerError).
  let principal = cache.get(bearer);
  if (!principal) {
    let resolved: { value: ResolvedPrincipal; cacheable: boolean };
    try {
      resolved = await resolvePrincipal(opts, bearer);
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
    principal = resolved.value;
    // Only cache a genuine whoami result. A transient backend failure yields a
    // least-privilege fallback that must NOT stick — re-probe next request so
    // the session self-corrects once the backend recovers.
    if (resolved.cacheable) cache.set(bearer, principal);
  }

  // The cold-path whoami probe above is awaited, so the client may have
  // disconnected meanwhile. If so, `res`'s "close" has already fired — bail
  // before building a transport whose teardown listener would never run.
  if (res.closed) return;

  // Stateless: a fresh server + transport per request, torn down when the
  // response closes. The SDK forbids reusing a stateless transport across
  // requests (message-id collisions), and a fresh transport with
  // sessionIdGenerator=undefined skips all session/initialize gating, so a
  // bare tools/call dispatches without a prior initialize on this instance.
  const client = buildClient(opts, bearer, principal);
  const server = buildServer({ client });
  const transport = new StreamableHTTPServerTransport({
    sessionIdGenerator: undefined,
  });
  res.on("close", () => {
    void transport.close();
    void server.close();
  });
  try {
    await server.connect(transport);
    await transport.handleRequest(req, res, req.body);
  } catch (err) {
    await transport.close().catch(() => undefined);
    await server.close().catch(() => undefined);
    throw err;
  }
}

/**
 * Construct the per-request E2AClient for an already-resolved principal.
 *
 * Why the agent-email default lives here and not in the SDK: the SDK is a thin
 * contract shared by CLI, browser, server, and Python users — auto-resolving an
 * agent on construction would be too magical for those callers. The MCP
 * transport has a clear notion of "the connecting credential," so pinning its
 * bound agent lets every tool call dispatch without forcing the LLM (or the
 * user) to pass `email` explicitly.
 */
function buildClient(
  opts: HttpServerOptions,
  bearer: string,
  principal: ResolvedPrincipal,
): McpClient {
  const { agentEmail, scope } = principal;
  if (opts.clientFactory) {
    // Preserve the no-arg-factory shape for callers (and tests) that don't
    // care about the resolved email/scope. Pass them through only when set.
    return agentEmail || scope
      ? opts.clientFactory(bearer, { ...(agentEmail ? { agentEmail } : {}), ...(scope ? { scope } : {}) })
      : opts.clientFactory(bearer);
  }
  return new McpClient(
    new E2AClient({ apiKey: bearer, baseUrl: opts.baseUrl }),
    agentEmail ?? "",
    // Fail CLOSED if scope is ever absent: "agent" is the least-privilege tier
    // (account = full admin surface). principal.scope is always set today, so
    // this only guards a future refactor — but a fail-open default here would
    // silently expose the admin surface.
    scope ?? "agent",
  );
}

/**
 * Resolve a bearer's scope + bound agent from whoami (GET /account), which the
 * backend scope-filters per credential (§6a). This drives both tool-tier gating
 * (server.ts) and the per-agent default:
 *   - agent scope   → pin the bound agent (whoami.agentAddress); the credential
 *     IS that agent. Surface = runtime tier.
 *   - account scope → no default agent (per §6a, explicit `email` required —
 *     the old single-agent auto-resolve is dropped). Surface = full.
 *
 * Throws {@link InvalidBearerError} on a 401 (revoked/expired/garbage token) so
 * the caller answers with the OAuth challenge. A non-auth failure (transient
 * backend hiccup) returns a least-privilege fallback marked `cacheable: false`
 * so it doesn't stick — the backend still enforces scope, and the next request
 * re-probes once the backend recovers.
 */
export async function resolvePrincipal(
  opts: HttpServerOptions,
  bearer: string,
): Promise<{ value: ResolvedPrincipal; cacheable: boolean }> {
  // The probe only calls whoami; its scope/agent don't matter. Build it bare
  // (factory(bearer) with no resolved opts) so the construction is
  // distinguishable from the final, resolved client.
  const probe = opts.clientFactory
    ? opts.clientFactory(bearer)
    : new McpClient(new E2AClient({ apiKey: bearer, baseUrl: opts.baseUrl }), "", "account");
  try {
    const me = await probe.whoami();
    const scope: Scope = me.scope === "account" ? "account" : "agent";
    const agentEmail = scope === "agent" && me.agentAddress ? me.agentAddress : undefined;
    return { value: { scope, ...(agentEmail ? { agentEmail } : {}) }, cacheable: true };
  } catch (err) {
    if (isUnauthorizedError(err)) {
      throw new InvalidBearerError();
    }
    // Fail-closed: least-privilege runtime tier, no default agent, not cached.
    return { value: { scope: "agent" }, cacheable: false };
  }
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
 * Start the HTTP server on `port`. Returns a closer that stops the listener —
 * wire it to SIGTERM in bin/http.ts. The server is stateless, so there are no
 * sessions to drain; closing the listener is the whole shutdown.
 */
export async function startHttpServer(
  port: number,
  opts: HttpServerOptions,
): Promise<{ close: () => Promise<void>; port: number }> {
  const { app, cache } = buildApp(opts);
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
      cache.clear();
    },
  };
}
