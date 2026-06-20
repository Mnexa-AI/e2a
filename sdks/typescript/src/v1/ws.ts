import WebSocket from "ws";
import { EventEmitter } from "node:events";
import { E2AError, E2AAuthError, E2APermissionError } from "./errors.js";

// Map a fatal (non-retryable) WebSocket handshake rejection status to a typed
// error — mirrors the Python SDK's _fatal_error_for_status (F6). A 4xx means the
// credential/request is wrong; reconnecting would loop forever, so the stream
// surfaces this and stops.
function fatalErrorForStatus(status: number): E2AError {
  const message = `WebSocket handshake rejected: HTTP ${status}`;
  if (status === 401) return new E2AAuthError({ code: "unauthorized", message, status, retryable: false });
  if (status === 403) return new E2APermissionError({ code: "forbidden", message, status, retryable: false });
  return new E2AError({ code: "websocket_rejected", message, status, retryable: false });
}

/**
 * A lightweight notification pushed by the e2a relay when new mail
 * arrives for an agent. Mirror of the Python SDK's `WSNotification`.
 *
 * The body is intentionally not included — fetch it via REST when (and
 * only when) you actually need it:
 *
 *     const email = await client.messages.get(notif.recipient, notif.message_id);
 */
export interface WSNotification {
  message_id: string;
  conversation_id?: string;
  from: string;
  /** Per-delivery target (this agent's address). */
  recipient: string;
  subject: string;
  received_at: string;
}

export interface WSListenerOptions {
  /** API key used as the `?token=` query parameter. */
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
  notification: [notification: WSNotification];
  open: [];
  close: [code: number, reason: string];
  error: [error: Error];
}

/**
 * Notification-only WebSocket listener.
 *
 * Connects to `/v1/agents/{address}/ws?token={apiKey}` and emits
 * `"notification"` events with lightweight metadata. The protocol is
 * server→client only — the client never sends application frames.
 *
 * Auth note: the API key currently rides in the `?token=` query parameter.
 * Query strings can leak into access logs and proxy traces, so this is a
 * known logged-credential limitation; moving auth to a header or a
 * short-lived connect ticket is a planned server-side change. No client
 * behavior changes when that lands — only this URL construction.
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
    this.url = `${wsBase}/v1/agents/${encodeURIComponent(opts.agentEmail)}/ws?token=${encodeURIComponent(opts.apiKey)}`;
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
    const ws = new WebSocket(this.url);
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
        const notif: WSNotification = JSON.parse(data.toString());
        this.emit("notification", notif);
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
 * Iterate for the happy path:
 *
 *     for await (const notif of client.listen()) {
 *       // …
 *     }
 *
 * Use `.on("error", …)` / `.on("close", …)` for connection-level
 * concerns. Call `.close()` to terminate iteration cleanly.
 */
export class WSStream extends EventEmitter<WSListenerEvents>
  implements AsyncIterable<WSNotification> {
  private readonly listener: WSListener;
  // Buffered notifications waiting to be yielded. Modest bound; if a
  // consumer is far behind we'd rather log loudly than balloon memory.
  private readonly buffer: WSNotification[] = [];
  // Pending iterator promises waiting for the next notification.
  private readonly waiters: Array<{
    resolve: (value: IteratorResult<WSNotification>) => void;
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

    this.listener.on("notification", (notif) => {
      this.emit("notification", notif);
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

  [Symbol.asyncIterator](): AsyncIterator<WSNotification> {
    return {
      next: (): Promise<IteratorResult<WSNotification>> => {
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
      return: (): Promise<IteratorResult<WSNotification>> => {
        this.close();
        return Promise.resolve({ value: undefined, done: true });
      },
    };
  }

  private deliver(notif: WSNotification): void {
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
