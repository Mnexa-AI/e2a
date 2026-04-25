import WebSocket from "ws";
import { EventEmitter } from "node:events";

/** A notification received over WebSocket. */
export interface WSNotification {
  message_id: string;
  from: string;
  to: string;
  subject: string;
  received_at: string;
  [key: string]: unknown;
}

export interface WSListenerOptions {
  /** API key used as the `?token=` query parameter. */
  apiKey: string;
  /** Agent email to listen for. */
  agentEmail: string;
  /** Base URL (http/https). Defaults to "https://e2a.dev". */
  baseUrl?: string;
  /** Auto-reconnect on disconnect. Defaults to true. */
  reconnect?: boolean;
  /** Reconnect delay in ms. Defaults to 1000. */
  reconnectDelay?: number;
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
 * Connects to `/api/v1/agents/{email}/ws?token={apiKey}` and emits
 * `"notification"` events. The client never sends application frames —
 * the protocol is server→client only.
 */
export class WSListener extends EventEmitter<WSListenerEvents> {
  private ws: WebSocket | null = null;
  private closed = false;
  private readonly url: string;
  private readonly shouldReconnect: boolean;
  private readonly reconnectDelay: number;

  constructor(private readonly opts: WSListenerOptions) {
    super();
    const base = (opts.baseUrl ?? "https://e2a.dev").replace(/\/+$/, "");
    const wsBase = base.replace(/^http/, "ws");
    this.url = `${wsBase}/api/v1/agents/${encodeURIComponent(opts.agentEmail)}/ws?token=${opts.apiKey}`;
    this.shouldReconnect = opts.reconnect ?? true;
    this.reconnectDelay = opts.reconnectDelay ?? 1000;
  }

  /** Open the WebSocket connection. */
  connect(): void {
    this.closed = false;
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

    ws.on("open", () => {
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
      if (!this.closed && this.shouldReconnect) {
        setTimeout(() => this.dial(), this.reconnectDelay);
      }
    });

    ws.on("error", (err: Error) => {
      this.emit("error", err);
    });

    this.ws = ws;
  }
}
