import { createHash } from "node:crypto";

import type { InboundEmail } from "@e2a/sdk/v1";

import type { ReplyAgent } from "../contracts.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../prompt.js";

export const ADK_APP_NAME = "e2a_email_assistant";

export interface ADKRunInput {
  prompt: string;
  userId: string;
  sessionId: string;
}

export type ADKRun = (input: ADKRunInput) => AsyncIterable<unknown>;

function canonicalMailbox(sender: string | null): string {
  const value = (sender ?? "").trim();
  const angleAddress = value.match(/<\s*([^<>]+?)\s*>\s*$/u)?.[1];
  return (angleAddress ?? value).trim().toLocaleLowerCase("en-US") || "missing-sender";
}

/** Derive an opaque identity while preventing session sharing across e2a inboxes. */
export function senderUserId(email: Pick<InboundEmail, "from" | "inbox">): string {
  const inbox = email.inbox.trim().toLocaleLowerCase("en-US") || "missing-inbox";
  const namespace = `${inbox}\0${canonicalMailbox(email.from)}`;
  const digest = createHash("sha256").update(namespace, "utf8").digest("hex").slice(0, 20);
  return `sender-${digest}`;
}

function finalResponse(event: unknown): boolean {
  if (!event || typeof event !== "object") return false;
  const isFinalResponse = (event as { isFinalResponse?: unknown }).isFinalResponse;
  return typeof isFinalResponse === "function" && Boolean(isFinalResponse.call(event));
}

function eventText(event: unknown): string {
  if (!event || typeof event !== "object") return "";
  const content = (event as { content?: unknown }).content;
  if (!content || typeof content !== "object") return "";
  const parts = (content as { parts?: unknown }).parts;
  if (!Array.isArray(parts)) return "";
  return parts.flatMap((part) => {
    if (!part || typeof part !== "object") return [];
    const text = (part as { text?: unknown }).text;
    return typeof text === "string" ? [text] : [];
  }).join("\n");
}

export class ADKReplyAgent implements ReplyAgent {
  constructor(private readonly run: ADKRun) {}

  async reply(email: InboundEmail): Promise<string> {
    let reply = "";
    const input = {
      prompt: emailPrompt(email),
      userId: senderUserId(email),
      sessionId: email.conversationId,
    };
    for await (const event of this.run(input)) {
      if (finalResponse(event)) reply = eventText(event);
    }
    return reply;
  }
}

interface ADKRunner {
  sessionService: {
    getOrCreateSession(input: {
      appName: string;
      userId: string;
      sessionId: string;
    }): Promise<unknown>;
  };
  runAsync(input: {
    userId: string;
    sessionId: string;
    newMessage: { role: "user"; parts: Array<{ text: string }> };
  }): AsyncIterable<unknown>;
}

export interface ADKSDK {
  LlmAgent: new (options: {
    name: string;
    model: string;
    instruction: string;
  }) => unknown;
  InMemoryRunner: new (options: { agent: unknown; appName: string }) => ADKRunner;
}

/** Build the production adapter from the official `@google/adk` exports. */
export function createADKReplyAgent(
  sdk: ADKSDK,
  env: Record<string, string | undefined> = process.env,
): ADKReplyAgent {
  const agent = new sdk.LlmAgent({
    name: "email_assistant",
    model: env.ADK_MODEL ?? "gemini-flash-latest",
    instruction: REPLY_INSTRUCTIONS,
  });
  const runner = new sdk.InMemoryRunner({ agent, appName: ADK_APP_NAME });

  return new ADKReplyAgent(async function* ({ prompt, userId, sessionId }) {
    await runner.sessionService.getOrCreateSession({
      appName: ADK_APP_NAME,
      userId,
      sessionId,
    });
    yield* runner.runAsync({
      userId,
      sessionId,
      newMessage: { role: "user", parts: [{ text: prompt }] },
    });
  });
}
