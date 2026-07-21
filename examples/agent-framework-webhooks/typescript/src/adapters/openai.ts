import type { InboundEmail } from "@e2a/sdk/v1";
import { Agent, run } from "@openai/agents";

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

/** Build the production adapter from the official `@openai/agents` exports. */
export function createOpenAIReplyAgent(
  env: Record<string, string | undefined> = process.env,
): OpenAIReplyAgent {
  const agent = new Agent({
    name: "Email assistant",
    instructions: REPLY_INSTRUCTIONS,
    model: env.OPENAI_MODEL ?? "gpt-5.6",
  });
  return new OpenAIReplyAgent(async (prompt) => {
    const result = await run(agent, prompt);
    return { finalOutput: result.finalOutput };
  });
}
