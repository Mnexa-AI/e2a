import WebSocket from "ws";
import { EventEmitter } from "node:events";
import { E2AError, E2AAuthError, E2APermissionError, E2ANotFoundError } from "./errors.js";
import type { WebhookEvent } from "./webhook-signature.js";

// Map a fatal (non-retryable) WebSocket handshake rejection status to a typed
// error — mirrors the Python SDK's _fatal_error_for_status (F6). A 4xx means the
// credential/request is wrong; reconnecting would loop forever, so the stream
// surfaces this and stops.
function fatalErrorForStatus(status: number): E2AError {
  const message = `WebSocket handshake rejected: HTTP ${status}`;
  if (status === 401) return new E2AAuthError({ code: "unauthorized", message, status, retryable: false });
  if (status === 403) return new E2APermissionError({ code: "forbidden", message, status, retryable: false });
  // 404 — the agent doesn't exist OR isn't yours (the server collapses the
  // cross-tenant case into not_found so the handshake can't enumerate agents).
  if (status === 404) return new E2ANotFoundError({ code: "not_found", message, status, retryable: false });
  return new E2AError({ code: "websocket_rejected", message, status, retryable: false });
}

/**
 * A WebSocket frame from the e2a relay: the SAME versioned event envelope a
 * webhook delivery carries — `{type, id, schema_version, created_at, data}`.
 * Today the relay emits `email.received` events (data: {@link EmailReceivedData});
 * tolerate unknown `type` values — future WS event kinds parse into the same
 * envelope. Narrow with {@link isEmailReceived} (or `event.type === "email.received"`).
 *
 * The body is intentionally not included — fetch it via REST when (and only
 * when) you actually need it. `client.webhooks.fetchMessage(event)` is the
 * bridge (the WS envelope is a WebhookEvent), or directly:
 *
 *     if (isEmailReceived(event)) {
 *       const email = await client.messages.get(event.data.delivered_to, event.data.message_id);
 *     }
 *
 * Mirror of the Python SDK's `WSEvent`.
 */
export type WSEvent = WebhookEvent;

export interface WSListenerOptions {
  /** API key, sent as the `Authorization: Bearer` handshake header. */
  apiKey: string;
  /** Agent email to listen for. */
  agentEmail: string;
  /** Base URL (http/https). Defaults to "https://api.e2a.dev". */
  baseUrl?: string;
  /**
   * Auto-reconnect on disconnect. Defaults to true.
   * Reconnect uses exponential backoff (1s → maxBackoffMs).
   */
  reconnect?: boolean;
  /** Initial reconnect delay in ms. Defaults to 1000 (1 second). */
  reconnectDelay?: number;
  /** Maximum reconnect delay in ms. Defaults to 30000 (30 seconds). */
  maxBackoffMs?: number;
}

export interface WSListenerEvents {
  event: [event: WSEvent];
  open: [];
  close: [code: number, reason: string];
  error: [error: Error];
}

/**
 * Notification-only WebSocket listener.
 *
 * Connects to `/v1/agents/{address}/ws` and emits `"event"` events, each a
 * versioned {@link WSEvent} envelope with lightweight metadata in `data`.
 * The protocol is server→client only — the client never sends application
 * frames.
 *
 * Auth: the API key is sent as the `Authorization: Bearer` handshake header, so
 * it never appears in the URL (no leak to access logs / proxy traces / Referer).
 *
 * For modern code, prefer {@link E2AClient.listen} which wraps this
 * class with an async-iteration-friendly API while still exposing the
 * EventEmitter interface for `error` / `open` / `close`.
 */
export class WSListener extends EventEmitter<WSListenerEvents> {
  private ws: WebSocket | null = null;
  private closed = false;
  private readonly url: string;
  private readonly shouldReconnect: boolean;
  private readonly initialDelayMs: number;
  private readonly maxBackoffMs: number;
  private currentBackoffMs: number;

  constructor(private readonly opts: WSListenerOptions) {
    super();
    const base = (opts.baseUrl ?? "https://api.e2a.dev").replace(/\/+$/, "");
    const wsBase = base.replace(/^http/, "ws");
    this.url = `${wsBase}/v1/agents/${encodeURIComponent(opts.agentEmail)}/ws`;
    this.shouldReconnect = opts.reconnect ?? true;
    this.initialDelayMs = opts.reconnectDelay ?? 1000;
    this.maxBackoffMs = opts.maxBackoffMs ?? 30_000;
    this.currentBackoffMs = this.initialDelayMs;
  }

  /** Open the WebSocket connection. */
  connect(): void {
    this.closed = false;
    this.currentBackoffMs = this.initialDelayMs;
    this.dial();
  }

  /** Close the connection permanently (no reconnect). */
  close(): void {
    this.closed = true;
    if (this.ws) {
      this.ws.close(1000, "client close");
      this.ws = null;
    }
  }

  private dial(): void {
    // Auth rides in the handshake header, never the URL — keeps the long-lived
    // API key out of access logs / proxy traces. Node's `ws` supports handshake
    // headers (a browser WebSocket could not, which is why this SDK targets Node).
    const ws = new WebSocket(this.url, {
      headers: { Authorization: `Bearer ${this.opts.apiKey}` },
    });
    // A fatal (4xx) handshake rejection — captured here, acted on in `close`.
    // The credential/request is wrong, so reconnecting would loop forever (F6).
    let fatal: E2AError | null = null;
    ws.on("unexpected-response", (_req, res: { statusCode?: number }) => {
      const status = res.statusCode ?? 0;
      if (status >= 400 && status < 500) fatal = fatalErrorForStatus(status);
    });

    ws.on("open", () => {
      // Successful connection — reset backoff so the next disconnect
      // starts fresh from the initial delay rather than continuing to
      // grow unbounded across multiple reconnect cycles.
      this.currentBackoffMs = this.initialDelayMs;
      this.emit("open");
    });

    ws.on("message", (data: WebSocket.RawData) => {
      try {
        const parsed: unknown = JSON.parse(data.toString());
        // Every frame is the versioned event envelope; a frame without a
        // string `type` is not one. Unknown `type` VALUES are fine (future
        // WS event kinds) — consumers narrow on type.
        if (!parsed || typeof parsed !== "object" || typeof (parsed as { type?: unknown }).type !== "string") {
          this.emit("error", new Error("WS frame is not an event envelope (missing string `type`)"));
          return;
        }
        this.emit("event", parsed as WSEvent);
      } catch (err) {
        this.emit("error", err instanceof Error ? err : new Error(String(err)));
      }
    });

    ws.on("close", (code: number, reason: Buffer) => {
      this.emit("close", code, reason.toString());
      this.ws = null;
      if (fatal) {
        // Fatal handshake (auth/4xx) — surface the typed error and STOP; a
        // reconnect would just loop on the same rejection (F6 parity with Python).
        this.closed = true;
        this.emit("error", fatal);
        return;
      }
      if (!this.closed && this.shouldReconnect) {
        setTimeout(() => this.dial(), this.currentBackoffMs);
        // Double the delay for the next reconnect, capped. Same shape
        // as Python's listen() backoff: 1s, 2s, 4s, 8s, …, capped.
        this.currentBackoffMs = Math.min(
          this.currentBackoffMs * 2,
          this.maxBackoffMs,
        );
      }
    });

    ws.on("error", (err: Error) => {
      // Suppress the noisy transport error that rides alongside a fatal
      // handshake — the typed error is emitted from `close`. Surface others.
      if (!fatal) this.emit("error", err);
    });

    this.ws = ws;
  }
}

/**
 * Hybrid AsyncIterable + EventEmitter returned by {@link E2AClient.listen}.
 *
 * Iterate for the happy path — each item is a {@link WSEvent} envelope:
 *
 *     for await (const event of client.listen("bot@acme.dev")) {
 *       if (!isEmailReceived(event)) continue; // tolerate future event kinds
 *       const email = await client.webhooks.fetchMessage(event);
 *     }
 *
 * Use `.on("error", …)` / `.on("close", …)` for connection-level
 * concerns. Call `.close()` to terminate iteration cleanly.
 */
export class WSStream extends EventEmitter<WSListenerEvents>
  implements AsyncIterable<WSEvent> {
  private readonly listener: WSListener;
  // Buffered notifications waiting to be yielded. Modest bound; if a
  // consumer is far behind we'd rather log loudly than balloon memory.
  private readonly buffer: WSEvent[] = [];
  // Pending iterator promises waiting for the next notification.
  private readonly waiters: Array<{
    resolve: (value: IteratorResult<WSEvent>) => void;
    reject: (err: Error) => void;
  }> = [];
  private closed = false;

  constructor(opts: WSListenerOptions) {
    super();
    this.listener = new WSListener(opts);

    // Forward connection-level events to consumers who prefer the
    // EventEmitter interface (or want both).
    this.listener.on("open", () => this.emit("open"));
    this.listener.on("close", (code, reason) => this.emit("close", code, reason));
    this.listener.on("error", (err) => {
      this.emit("error", err);
      // A typed (fatal) error — e.g. an auth/4xx handshake rejection — ends the
      // stream: the listener has stopped reconnecting, so mark closed too, then
      // reject in-flight awaits so the for-await throws the typed error rather
      // than hanging on a stream that will never deliver again (F6).
      if (err instanceof E2AError) this.closed = true;
      this.drainWaitersWithError(err);
    });

    this.listener.on("event", (notif) => {
      this.emit("event", notif);
      this.deliver(notif);
    });

    this.listener.connect();
  }

  /** Close the connection and end iteration. */
  close(): void {
    this.closed = true;
    this.listener.close();
    // Resolve any in-flight awaits with done=true so the loop exits.
    while (this.waiters.length > 0) {
      this.waiters.shift()!.resolve({ value: undefined, done: true });
    }
  }

  [Symbol.asyncIterator](): AsyncIterator<WSEvent> {
    return {
      next: (): Promise<IteratorResult<WSEvent>> => {
        if (this.buffer.length > 0) {
          return Promise.resolve({ value: this.buffer.shift()!, done: false });
        }
        if (this.closed) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise((resolve, reject) => {
          this.waiters.push({ resolve, reject });
        });
      },
      return: (): Promise<IteratorResult<WSEvent>> => {
        this.close();
        return Promise.resolve({ value: undefined, done: true });
      },
    };
  }

  private deliver(notif: WSEvent): void {
    if (this.waiters.length > 0) {
      this.waiters.shift()!.resolve({ value: notif, done: false });
    } else {
      this.buffer.push(notif);
    }
  }

  private drainWaitersWithError(err: Error): void {
    while (this.waiters.length > 0) {
      this.waiters.shift()!.reject(err);
    }
  }
}
