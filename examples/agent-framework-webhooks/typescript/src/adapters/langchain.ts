import type { InboundEmail } from "@e2a/sdk/v1";
import { createAgent } from "langchain";

import type { ReplyAgent } from "../contracts.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../prompt.js";

export interface LangChainResult {
  messages?: readonly unknown[];
}

export type LangChainRun = (prompt: string) => Promise<LangChainResult>;

function field(value: unknown, name: "type" | "role" | "content" | "text"): unknown {
  if (!value || typeof value !== "object") return undefined;
  return (value as Record<string, unknown>)[name];
}

function textContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.flatMap((block) => {
    const text = field(block, "text");
    return field(block, "type") === "text" && typeof text === "string" ? [text] : [];
  }).join("\n");
}

export class LangChainReplyAgent implements ReplyAgent {
  constructor(private readonly run: LangChainRun) {}

  async reply(email: InboundEmail, _conversationId: string): Promise<string> {
    const result = await this.run(emailPrompt(email));
    for (const message of [...(result.messages ?? [])].reverse()) {
      if (field(message, "type") === "ai" || field(message, "role") === "assistant") {
        return textContent(field(message, "content"));
      }
    }
    throw new Error("LangChain result did not contain an assistant message");
  }
}

/** Build the production adapter from LangChain's official `createAgent` export. */
export function createLangChainReplyAgent(
  env: Record<string, string | undefined> = process.env,
): LangChainReplyAgent {
  const agent = createAgent({
    model: env.LANGCHAIN_MODEL ?? "openai:gpt-5.4",
    tools: [],
    systemPrompt: REPLY_INSTRUCTIONS,
  });
  return new LangChainReplyAgent(async (prompt) => {
    const result = await agent.invoke({ messages: [{ role: "user", content: prompt }] });
    return { messages: result.messages };
  });
}
