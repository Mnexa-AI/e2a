import { createHash } from "node:crypto";

import type { InboundEmail } from "@e2a/sdk/v1";
import { InMemoryRunner, LlmAgent, isFinalResponse, type Event } from "@google/adk";

import type { ReplyAgent } from "../contracts.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../prompt.js";

export const ADK_APP_NAME = "e2a_email_assistant";

export interface ADKRunInput {
  prompt: string;
  userId: string;
  sessionId: string;
}

export type ADKRun = (input: ADKRunInput) => AsyncIterable<Event>;

function canonicalMailbox(sender: string | null): string {
  const value = (sender ?? "").trim();
  const angleAddress = value.match(/<\s*([^<>\s]+@[^<>\s]+)\s*>/u)?.[1];
  return (angleAddress ?? value).trim().toLowerCase() || "missing-sender";
}

/** Derive an opaque identity while preventing session sharing across e2a inboxes. */
export function senderUserId(email: Pick<InboundEmail, "from" | "inbox">): string {
  const inbox = email.inbox.trim().toLocaleLowerCase("en-US") || "missing-inbox";
  const namespace = `${inbox}\0${canonicalMailbox(email.from)}`;
  const digest = createHash("sha256").update(namespace, "utf8").digest("hex").slice(0, 20);
  return `sender-${digest}`;
}

function eventText(event: Event): string {
  return event.content?.parts?.flatMap((part) =>
    typeof part.text === "string" ? [part.text] : [],
  ).join("\n") ?? "";
}

export class ADKReplyAgent implements ReplyAgent {
  constructor(private readonly run: ADKRun) {}

  async reply(email: InboundEmail, conversationId: string): Promise<string> {
    let reply = "";
    const input = {
      prompt: emailPrompt(email),
      userId: senderUserId(email),
      sessionId: conversationId,
    };
    for await (const event of this.run(input)) {
      if (isFinalResponse(event)) reply = eventText(event);
    }
    return reply;
  }
}

/** Build the production adapter from the official `@google/adk` exports. */
export function createADKReplyAgent(
  env: Record<string, string | undefined> = process.env,
): ADKReplyAgent {
  const agent = new LlmAgent({
    name: "email_assistant",
    model: env.ADK_MODEL ?? "gemini-flash-latest",
    instruction: REPLY_INSTRUCTIONS,
  });
  const runner = new InMemoryRunner({ agent, appName: ADK_APP_NAME });

  return new ADKReplyAgent(async function* ({ prompt, userId, sessionId }) {
    const sessionContext = {
      appName: ADK_APP_NAME,
      userId,
      sessionId,
    };
    const session = await runner.sessionService.getSession(sessionContext);
    if (session === undefined) await runner.sessionService.createSession(sessionContext);
    yield* runner.runAsync({
      userId,
      sessionId,
      newMessage: { role: "user", parts: [{ text: prompt }] },
    });
  });
}
