import type { InboundEmail } from "@e2a/sdk/v1";

import type { ReplyAgent } from "../contracts.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../prompt.js";

export interface AnthropicResult {
  content: readonly unknown[];
}

export type AnthropicRun = (prompt: string) => Promise<AnthropicResult>;

function textBlock(block: unknown): string | undefined {
  if (!block || typeof block !== "object") return undefined;
  const value = block as { type?: unknown; text?: unknown };
  return value.type === "text" && typeof value.text === "string" ? value.text : undefined;
}

export class AnthropicReplyAgent implements ReplyAgent {
  constructor(private readonly run: AnthropicRun) {}

  async reply(email: InboundEmail): Promise<string> {
    const result = await this.run(emailPrompt(email));
    return result.content.flatMap((block) => {
      const text = textBlock(block);
      return text === undefined ? [] : [text];
    }).join("\n");
  }
}

interface AnthropicClient {
  messages: {
    create(input: {
      model: string;
      max_tokens: number;
      system: string;
      messages: Array<{ role: "user"; content: string }>;
    }): Promise<AnthropicResult>;
  };
}

export interface AnthropicSDK {
  Anthropic: new () => AnthropicClient;
}

/** Build the production adapter from the official `@anthropic-ai/sdk` export. */
export function createAnthropicReplyAgent(
  sdk: AnthropicSDK,
  env: Record<string, string | undefined> = process.env,
): AnthropicReplyAgent {
  const client = new sdk.Anthropic();
  return new AnthropicReplyAgent((prompt) => client.messages.create({
    model: env.ANTHROPIC_MODEL ?? "claude-opus-4-8",
    max_tokens: 1024,
    system: REPLY_INSTRUCTIONS,
    messages: [{ role: "user", content: prompt }],
  }));
}
