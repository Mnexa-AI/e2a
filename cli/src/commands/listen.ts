import type { E2AClient, WSNotification } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";
import { EXIT, fail } from "../exit.js";
import { sanitizeTsvField } from "./messages.js";

const MAX_TIMEOUT_MS = 2 ** 31 - 1; // setTimeout clamps larger delays to ~1ms

/**
 * Membership check WITHOUT fetching the message: GET /messages/{id} marks a
 * message read as a side effect, so filtering by fetching would silently
 * consume messages from OTHER conversations out of any unread-queue consumer.
 * list() has no side effects; scoped by conversation + a since just before
 * the notification, it's a single small page.
 */
async function inConversation(
  client: E2AClient,
  agentEmail: string,
  notification: WSNotification,
  conversationId: string,
): Promise<boolean> {
  const receivedMs = Date.parse(notification.received_at);
  const since = Number.isNaN(receivedMs)
    ? undefined
    : new Date(receivedMs - 2000).toISOString();
  let scanned = 0;
  for await (const m of client.messages.list(agentEmail, {
    conversationId,
    since,
    readStatus: "all",
    sort: "asc",
    limit: 100,
  })) {
    if (m.messageId === notification.message_id) return true;
    if (++scanned >= 500) break; // NaN-since safety bound
  }
  return false;
}

export interface ListenOptions {
  agent?: string;
  json?: boolean;
  forward?: string;
  forwardToken?: string;
  /** Only surface messages belonging to this conversation id. */
  conversation?: string;
  /** Exit 0 after the first (matching) message — the blocking-wait primitive. */
  once?: boolean;
  /** RFC3339 deadline for --once; expiry prints TIMEOUT and exits 6. */
  until?: string;
  /** With --once: print the message's body text instead of a summary/JSON. */
  text?: boolean;
}

export async function listen(opts: ListenOptions): Promise<void> {
  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);

  let deadlineMs: number | undefined;
  if (opts.until) {
    deadlineMs = Date.parse(opts.until);
    if (Number.isNaN(deadlineMs)) fail(EXIT.USAGE, `--until must be an RFC3339 timestamp, got: ${opts.until}`);
  }
  if ((opts.until || opts.text) && !opts.once) {
    fail(EXIT.USAGE, "--until and --text require --once");
  }

  process.stderr.write(`Listening for emails to ${agentEmail}...\n`);

  // client.listen() returns a WSStream — both AsyncIterable<WSNotification>
  // and EventEmitter. We use both: events for connection lifecycle, the
  // for-await loop for the happy path.
  const stream = client.listen(agentEmail);

  // Deadline: closing the stream ends the for-await loop; the flag tells the
  // post-loop code this was expiry, not a server-side close. exitCode (not
  // process.exit) so pending stdout flushes.
  let timedOut = false;
  let deadlineTimer: ReturnType<typeof setTimeout> | undefined;
  if (deadlineMs !== undefined) {
    if (deadlineMs - Date.now() <= 0) {
      process.stdout.write("TIMEOUT\n");
      process.exitCode = EXIT.TIMEOUT;
      stream.close();
      return;
    }
    // Chain timers instead of one setTimeout: Node clamps delays > 2^31-1 ms
    // (~24.8 days) to ~1ms, which would fire TIMEOUT instantly for a
    // far-future --until.
    const armDeadline = () => {
      const wait = (deadlineMs as number) - Date.now();
      if (wait <= 0) {
        timedOut = true;
        stream.close();
        return;
      }
      deadlineTimer = setTimeout(armDeadline, Math.min(wait, MAX_TIMEOUT_MS));
      deadlineTimer.unref?.();
    };
    armDeadline();
  }

  stream.on("open", () => {
    process.stderr.write("Connected.\n");
  });

  stream.on("close", (code: number, reason: string) => {
    process.stderr.write(`WS closed: code=${code} reason="${reason}"\n`);
  });

  stream.on("error", (err: Error) => {
    process.stderr.write(`Connection error: ${err.message}\n`);
  });

  process.on("SIGINT", () => {
    stream.close();
    process.stderr.write("\nDisconnecting...\n");
    process.exit(0);
  });

  // Iterate notifications. handleNotification swallows its own errors so
  // a single bad message doesn't tear down the loop.
  let matched = false;
  for await (const notification of stream) {
    try {
      // Filter first, via list() — never get(), which marks messages read
      // (silently consuming OTHER conversations' messages was the bug).
      if (opts.conversation) {
        const ok = await inConversation(client, agentEmail, notification, opts.conversation);
        if (!ok) continue;
      }
      if (opts.once) {
        // --forward still delivers under --once; the blocking wait must not
        // silently drop the webhook side channel.
        if (opts.forward) {
          await handleNotification(client, agentEmail, notification, opts);
        }
        if (opts.text) {
          const full = await client.messages.get(agentEmail, notification.message_id);
          process.stdout.write((full.parsed?.text ?? full.body?.text ?? "").trim() + "\n");
        } else if (opts.json) {
          const full = await client.messages.get(agentEmail, notification.message_id);
          process.stdout.write(JSON.stringify(full) + "\n");
        } else {
          // One stable machine shape for the blocking wait, regardless of
          // whether --conversation was used.
          process.stdout.write(
            `${notification.message_id}\t${sanitizeTsvField(notification.from)}\t${notification.received_at}\n`,
          );
        }
        matched = true;
        break;
      }
      await handleNotification(client, agentEmail, notification, opts);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`Error handling message: ${message}\n`);
    }
  }

  if (deadlineTimer) clearTimeout(deadlineTimer);
  stream.close();
  if (opts.once && !matched) {
    if (timedOut) {
      // Clean window expiry — the documented exit-6 outcome.
      process.stdout.write("TIMEOUT\n");
      process.exitCode = EXIT.TIMEOUT;
    } else {
      // The stream ended before the deadline (handshake rejected, server
      // close). That's a transient error, NOT a timeout — callers polling in
      // a loop must be able to tell the difference or they spin hot.
      process.stderr.write("stream closed before the deadline\n");
      process.exitCode = EXIT.ERROR;
    }
  }
}

export async function handleNotification(
  client: E2AClient,
  agentEmail: string,
  notification: WSNotification,
  opts: Pick<ListenOptions, "json" | "forward" | "forwardToken">,
): Promise<void> {
  if (opts.json) {
    const full = await client.messages.get(agentEmail, notification.message_id);
    process.stdout.write(JSON.stringify(full) + "\n");
    return;
  }

  if (opts.forward) {
    await forwardMessage(
      client,
      agentEmail,
      notification,
      opts.forward,
      opts.forwardToken,
    );
    return;
  }

  const time = new Date(notification.received_at).toLocaleTimeString();
  process.stdout.write(
    `[${time}] From: ${notification.from} | Subject: ${notification.subject}\n`,
  );
}

export function isOpenClawUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    return parsed.pathname.endsWith("/v1/responses");
  } catch {
    return false;
  }
}

export async function forwardMessage(
  client: E2AClient,
  agentEmail: string,
  notification: WSNotification,
  forwardUrl: string,
  forwardToken?: string,
): Promise<void> {
  // Fetch full message (MessageView).
  const full = await client.messages.get(agentEmail, notification.message_id);

  let fetchBody: string;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (isOpenClawUrl(forwardUrl)) {
    // Prefer the server's parsed/plain-text body; fall back to decoding
    // the raw RFC 2822 message for OpenClaw.
    let body = full.parsed?.text || full.body?.text || "";
    if (!body && full.rawMessage) {
      try {
        const decoded = Buffer.from(full.rawMessage, "base64").toString("utf-8");
        body = extractTextFromRaw(decoded);
      } catch {
        // Fall back to empty body
      }
    }

    const message = `New email from ${notification.from}\n\nSubject: ${notification.subject}\n\n${body}`;

    fetchBody = JSON.stringify({
      model: "openclaw",
      input: message,
    });

    if (forwardToken) {
      headers["Authorization"] = `Bearer ${forwardToken}`;
    }
  } else {
    // Generic forward — POST the full v1 MessageView as JSON. NOTE: in 3.0 this
    // is the SDK's camelCase model shape (messageId, createdAt, …), not the
    // legacy snake_case wire JSON; --forward consumers updating to the 3.0 CLI
    // should read the v1 field names.
    fetchBody = JSON.stringify(full);
    if (forwardToken) {
      headers["Authorization"] = `Bearer ${forwardToken}`;
    }
  }

  const res = await fetch(forwardUrl, {
    method: "POST",
    headers,
    body: fetchBody,
  });

  if (!res.ok) {
    process.stderr.write(
      `Forward failed (${res.status}): ${await res.text()}\n`,
    );
    return;
  }

  process.stderr.write(
    `Forwarded ${notification.message_id} to ${forwardUrl}\n`,
  );

  // If OpenClaw, extract reply and send it back
  if (isOpenClawUrl(forwardUrl)) {
    const responseText = await extractResponseText(res);
    if (responseText) {
      try {
        await client.messages.reply(agentEmail, notification.message_id, {
          body: responseText,
        });
        process.stderr.write(
          `Replied to ${notification.from} (${notification.message_id})\n`,
        );
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err);
        process.stderr.write(`Reply failed: ${message}\n`);
      }
    }
  }
}

/** Extract text body from raw RFC 2822 message (lightweight, no mailparser). */
function extractTextFromRaw(raw: string): string {
  const headerEnd = raw.indexOf("\r\n\r\n");
  let body: string;
  if (headerEnd === -1) {
    const altEnd = raw.indexOf("\n\n");
    if (altEnd === -1) return raw;
    body = raw.slice(altEnd + 2).trim();
  } else {
    body = raw.slice(headerEnd + 4).trim();
  }

  // Check for MIME multipart — extract the text/plain part
  const ctHeader = raw.slice(0, headerEnd === -1 ? raw.indexOf("\n\n") : headerEnd);
  const boundaryMatch = ctHeader.match(/boundary="?([^"\s;]+)"?/i);
  if (boundaryMatch) {
    const boundary = boundaryMatch[1];
    const parts = body.split(`--${boundary}`);
    for (const part of parts) {
      if (/content-type:\s*text\/plain/i.test(part)) {
        const partBodyStart = part.indexOf("\r\n\r\n") !== -1
          ? part.indexOf("\r\n\r\n") + 4
          : part.indexOf("\n\n") !== -1
            ? part.indexOf("\n\n") + 2
            : -1;
        if (partBodyStart !== -1) {
          return part.slice(partBodyStart).trim();
        }
      }
    }
    // Fallback: first non-empty part body
    for (const part of parts) {
      const trimmed = part.trim();
      if (trimmed && !trimmed.startsWith("--") && trimmed.includes("\n")) {
        const partBodyStart = trimmed.indexOf("\r\n\r\n") !== -1
          ? trimmed.indexOf("\r\n\r\n") + 4
          : trimmed.indexOf("\n\n") !== -1
            ? trimmed.indexOf("\n\n") + 2
            : -1;
        if (partBodyStart !== -1) {
          return trimmed.slice(partBodyStart).trim();
        }
      }
    }
  }

  return body;
}

// Extract text from an OpenAI Responses API response.
export async function extractResponseText(res: Response): Promise<string | null> {
  try {
    const json = await res.json();
    if (!json.output || !Array.isArray(json.output)) return null;

    const parts: string[] = [];
    for (const item of json.output) {
      if (item.type !== "message" || !Array.isArray(item.content)) continue;
      for (const block of item.content) {
        if (block.type === "output_text" && block.text) {
          parts.push(block.text);
        }
      }
    }
    return parts.length > 0 ? parts.join("\n") : null;
  } catch {
    return null;
  }
}
