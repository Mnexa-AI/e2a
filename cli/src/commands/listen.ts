import type { E2AClient, EmailReceivedData, WSEvent } from "@e2a/sdk/v1";
import { E2AConnectionReplacedError, isEmailReceived } from "@e2a/sdk/v1";
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
  notification: EmailReceivedData,
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
    if (m.id === notification.message_id) return true;
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

  // client.listen() returns a WSStream — both AsyncIterable<WSEvent> (the
  // versioned event envelope, same shape as a webhook delivery) and
  // EventEmitter. We use both: events for connection lifecycle, the
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
    // The replaced takeover (close code 4000) gets its dedicated message from
    // the for-await catch below — don't print it twice.
    if (err instanceof E2AConnectionReplacedError) return;
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
  try {
  for await (const event of stream) {
    try {
      // Only email.received frames carry an inbox notification; tolerate (and
      // skip) unknown event kinds — forward-compat with future WS events.
      if (!isEmailReceived(event)) continue;
      const notification = event.data;
      // Filter first, via list() — never get(), which marks messages read
      // (silently consuming OTHER conversations' messages was the bug).
      if (opts.conversation) {
        const ok = await inConversation(client, agentEmail, notification, opts.conversation);
        if (!ok) continue;
      }
      if (opts.once) {
        // --forward still delivers under --once (a side channel that must not
        // be silently dropped). Call forwardMessage directly — NOT
        // handleNotification, which also renders stdout and would double-print
        // under --json, since the --once block renders below.
        if (opts.forward) {
          await forwardMessage(
            client,
            agentEmail,
            notification,
            opts.forward,
            opts.forwardToken,
          );
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
  } catch (err: unknown) {
    // Terminal stream errors thrown by the for-await (the SDK stopped
    // reconnecting): give the replaced takeover its own clear exit; let other
    // typed errors ride the top-level handler's AUTH/REQUEST/ERROR mapping.
    if (deadlineTimer) clearTimeout(deadlineTimer);
    stream.close();
    if (err instanceof E2AConnectionReplacedError) {
      // EXIT.REQUEST (5), not ERROR (1): retry-on-1 wrappers must NOT rerun
      // this — reconnecting would steal the socket back from the newer
      // listener and loop (the exact bug the 4000 close code exists to stop).
      fail(
        EXIT.REQUEST,
        'listener replaced: a newer WebSocket connection for this agent took over (close code 4000 "replaced"). ' +
          "Not reconnecting — the server keeps one connection per agent; stop the other listener if this one should win.",
      );
    }
    throw err;
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
  notification: EmailReceivedData,
  opts: Pick<ListenOptions, "json" | "forward" | "forwardToken">,
): Promise<void> {
  // Forward is an independent SIDE CHANNEL: it must fire whenever --forward is
  // set, regardless of how (or whether) the message is also rendered to stdout.
  // Gating it behind an `else` after --json silently dropped the forward when
  // both flags were passed — the exact silent side-channel drop the CLI's
  // exit-code contract exists to prevent.
  if (opts.forward) {
    await forwardMessage(
      client,
      agentEmail,
      notification,
      opts.forward,
      opts.forwardToken,
    );
  }

  if (opts.json) {
    const full = await client.messages.get(agentEmail, notification.message_id);
    process.stdout.write(JSON.stringify(full) + "\n");
    return;
  }

  // Forward-only mode keeps stdout clean (forwardMessage logs to stderr); the
  // human summary is only for the plain (non-json, non-forward) default.
  if (opts.forward) return;

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
  notification: EmailReceivedData,
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
    // is the SDK's camelCase model shape (id, createdAt, …), not the
    // legacy snake_case wire JSON; --forward consumers updating to the 3.0 CLI
    // should read the v1 field names.
    fetchBody = JSON.stringify(full);
    if (forwardToken) {
      headers["Authorization"] = `Bearer ${forwardToken}`;
    }
  }

  let res: Response;
  try {
    res = await fetch(forwardUrl, {
      method: "POST",
      headers,
      body: fetchBody,
    });
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : String(err);
    process.stderr.write(`Forward failed: ${message}\n`);
    return;
  }

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
          text: responseText,
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
