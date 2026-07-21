import type { InboundEmail } from "@e2a/sdk/v1";

import type { ReplyAgent } from "../contracts.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../prompt.js";

export interface OpenAIResult {
  finalOutput?: unknown;
}

export type OpenAIRun = (prompt: string) => Promise<OpenAIResult>;

export class OpenAIReplyAgent implements ReplyAgent {
  constructor(private readonly run: OpenAIRun) {}

  async reply(email: InboundEmail): Promise<string> {
    const result = await this.run(emailPrompt(email));
    return result.finalOutput == null ? "" : String(result.finalOutput);
  }
}

export interface OpenAISDK {
  Agent: new (options: { name: string; instructions: string; model: string }) => unknown;
  run(agent: unknown, prompt: string): Promise<OpenAIResult>;
}

/** Build the production adapter from the official `@openai/agents` exports. */
export function createOpenAIReplyAgent(
  sdk: OpenAISDK,
  env: Record<string, string | undefined> = process.env,
): OpenAIReplyAgent {
  const agent = new sdk.Agent({
    name: "Email assistant",
    instructions: REPLY_INSTRUCTIONS,
    model: env.OPENAI_MODEL ?? "gpt-5.6",
  });
  return new OpenAIReplyAgent((prompt) => sdk.run(agent, prompt));
}
