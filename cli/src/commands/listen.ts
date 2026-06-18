import type { E2AClient, WSNotification } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

export interface ListenOptions {
  agent?: string;
  json?: boolean;
  forward?: string;
  forwardToken?: string;
}

export async function listen(opts: ListenOptions): Promise<void> {
  const client = createClient({ from: opts.agent });
  const agentEmail = requireAgentEmail(opts.agent);

  process.stderr.write(`Listening for emails to ${agentEmail}...\n`);

  // client.listen() returns a WSStream — both AsyncIterable<WSNotification>
  // and EventEmitter. We use both: events for connection lifecycle, the
  // for-await loop for the happy path.
  const stream = client.listen(agentEmail);

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
  for await (const notification of stream) {
    try {
      await handleNotification(client, agentEmail, notification, opts);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`Error handling message: ${message}\n`);
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
